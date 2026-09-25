// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

var defaultSkuPriority = []string{"GlobalStandard", "DataZoneStandard", "Standard"}

// defaultAgentModel is preselected in interactive catalog prompts.
const defaultAgentModel = "gpt-5.4-mini"

// defaultVoiceModel is the managed speech-to-speech model used for a
// prompt-voice agent when --model is not supplied.
const defaultVoiceModel = "gpt-realtime"

// defaultDeploymentCapacity is the preferred deployment capacity for agent model deployments.
// This overrides the lower SKU default (typically 10) which is insufficient for agents.
const defaultDeploymentCapacity int32 = 50

// errModelSkipped is returned when the user explicitly skips a model.
var errModelSkipped = errors.New("user skipped model")

// existingDeploymentError wraps a project.Deployment selected by the user
// from the project's existing deployments inside getModelDetails. The caller
// (getModelDeploymentDetails) detects this via errors.As and returns the
// deployment with isNew=false.
type existingDeploymentError struct {
	Deployment *project.Deployment
}

func (e *existingDeploymentError) Error() string {
	return "user selected existing deployment"
}

func (a *modelSelector) loadAiCatalog(ctx context.Context) error {
	if a.modelCatalog != nil {
		return nil
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text:        "Loading the model catalog",
		ClearOnStop: true,
	})

	if err := spinner.Start(ctx); err != nil {
		return fmt.Errorf("failed to start spinner: %w", err)
	}

	modelResp, err := a.azdClient.Ai().ListModels(ctx, &azdext.ListModelsRequest{
		AzureContext: a.azureContext,
		Filter:       agentModelFilter(nil, nil),
	})
	stopErr := spinner.Stop(ctx)
	if err != nil {
		return exterrors.FromAiService(err, exterrors.CodeModelCatalogFailed)
	}
	if stopErr != nil {
		return stopErr
	}

	a.modelCatalog = mapModelsByName(modelResp.Models)

	return nil
}

func mapModelsByName(models []*azdext.AiModel) map[string]*azdext.AiModel {
	modelMap := make(map[string]*azdext.AiModel, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		modelMap[model.Name] = model
	}

	return modelMap
}

func (a *modelSelector) updateEnvLocation(ctx context.Context, selectedLocation string) error {
	envName := ""
	if a.environment != nil {
		envName = a.environment.Name
	} else {
		envResponse, err := a.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return fmt.Errorf("failed to get current azd environment: %w", err)
		}
		envName = envResponse.Environment.Name
	}

	_, err := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envName,
		Key:     "AZURE_AI_DEPLOYMENTS_LOCATION",
		Value:   selectedLocation,
	})
	if err != nil {
		return fmt.Errorf("failed to update AZURE_AI_DEPLOYMENTS_LOCATION in azd environment: %w", err)
	}

	if a.azureContext == nil {
		a.azureContext = &azdext.AzureContext{}
	}
	if a.azureContext.Scope == nil {
		a.azureContext.Scope = &azdext.AzureScope{}
	}
	a.azureContext.Scope.Location = selectedLocation

	fmt.Println(output.WithSuccessFormat("Updated AZURE_AI_DEPLOYMENTS_LOCATION to '%s' in your azd environment.", selectedLocation))
	return nil
}

func (a *modelSelector) getModelDetails(
	ctx context.Context, modelName string, allowSkip bool,
) (*azdext.AiModelDeployment, error) {
	if err := a.loadAiCatalog(ctx); err != nil {
		return nil, err
	}

	model, exists := a.modelCatalog[modelName]
	if !exists {
		selectedModel, err := a.promptForAlternativeModel(ctx, modelName)
		if err != nil {
			return nil, err
		}
		if selectedModel == nil {
			return nil, fmt.Errorf("no model selected, exiting")
		}
		model = selectedModel
	} else if !a.flags.noPrompt {
		// Model found in catalog -- let user confirm, choose a different one,
		// or (when allowed) skip the model entirely. This is the standard
		// selector for both generated definitions and unified azure.yaml input.
		choices := []*azdext.SelectChoice{
			{Label: fmt.Sprintf("Use '%s'", model.Name), Value: "keep"},
			{Label: "Choose a different model to deploy", Value: "change"},
		}
		if len(a.allDeployments) > 0 {
			choices = append(choices, &azdext.SelectChoice{
				Label: "Use an existing deployment from this project",
				Value: "use_existing",
			})
		}
		if allowSkip {
			choices = append(choices, &azdext.SelectChoice{
				Label: "Skip this model (do not deploy)",
				Value: "skip",
			})
		}

		defaultIdx := int32(0)
		resp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:       fmt.Sprintf("Model '%s' is selected.", model.Name),
				Choices:       choices,
				SelectedIndex: &defaultIdx,
			},
		})
		if err != nil {
			if exterrors.IsCancellation(err) {
				return nil, exterrors.Cancelled("model selection was cancelled")
			}
			return nil, fmt.Errorf("failed to prompt for model choice: %w", err)
		}

		switch choices[*resp.Value].Value {
		case "change":
			selectedModel, err := a.promptModelFromCatalog(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to select alternative model: %w", err)
			}
			if selectedModel == nil {
				return nil, fmt.Errorf("no model selected, exiting")
			}
			model = selectedModel
		case "use_existing":
			deployment, err := a.promptExistingDeployment(ctx)
			if err != nil {
				if exterrors.IsCancellation(err) {
					return nil, err
				}
				return nil, fmt.Errorf("failed to select existing deployment: %w", err)
			}
			return nil, &existingDeploymentError{Deployment: deployment}
		case "skip":
			fmt.Println(output.WithWarningFormat(
				"Skipped model '%s'. The agent will not have a model deployed.", model.Name))
			fmt.Println(output.WithGrayFormat(
				"Configure your agent's model manually before running 'azd provision'."))
			return nil, errModelSkipped
		}
	}

	currentLocation := a.azureContext.Scope.Location
	if !slices.Contains(model.Locations, currentLocation) {
		resolvedModel, resolvedLocation, err := a.promptForModelLocationMismatch(
			ctx,
			model,
			currentLocation,
			fmt.Sprintf("The model '%s' is not available in your current location '%s'.", model.Name, currentLocation),
			modelRecoveryReasonAvailability,
		)
		if err != nil {
			return nil, err
		}
		if resolvedModel == nil {
			return nil, fmt.Errorf("model unavailable in current location and no alternative selected, exiting")
		}
		model = resolvedModel
		currentLocation = resolvedLocation
	}

	if a.flags.noPrompt {
		fmt.Println("No prompt mode enabled, automatically selecting a model deployment based on availability and quota...")
		return resolveModelDeployment(ctx, a.azdClient, a.azureContext, model, currentLocation)
	}

	for {
		deploymentResp, err := a.azdClient.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
			AzureContext: a.azureContext,
			ModelName:    model.Name,
			Options: &azdext.AiModelDeploymentOptions{
				Locations: []string{currentLocation},
				Capacity:  new(defaultDeploymentCapacity),
			},
			Quota: &azdext.QuotaCheckOptions{
				MinRemainingCapacity: 1,
			},
		})
		if err == nil {
			return deploymentResp.Deployment, nil
		}

		if !isRecoverableDeploymentSelectionError(err) {
			return nil, exterrors.FromPrompt(err, "failed to prompt for model deployment")
		}

		resolvedModel, resolvedLocation, resolveErr := a.promptForModelLocationMismatch(
			ctx,
			model,
			currentLocation,
			fmt.Sprintf(
				"Not enough available quota to deploy model '%s' in '%s'.",
				model.Name,
				currentLocation,
			),
			modelRecoveryReasonQuota,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if resolvedModel == nil {
			return nil, fmt.Errorf("model unavailable due to quota constraints and no alternative selected, exiting")
		}

		model = resolvedModel
		currentLocation = resolvedLocation
	}
}

func resolveNoPromptCapacity(candidate *azdext.AiModelDeployment) (int32, bool) {
	capacity := candidate.Capacity
	defaulted := capacity <= 0
	if defaulted {
		capacity = defaultDeploymentCapacity
	}

	if candidate.Sku.CapacityStep > 0 && capacity%candidate.Sku.CapacityStep != 0 {
		step := candidate.Sku.CapacityStep
		capacity = ((capacity + step - 1) / step) * step
	}

	if candidate.Sku.MinCapacity > 0 && capacity < candidate.Sku.MinCapacity {
		capacity = candidate.Sku.MinCapacity
	}
	if candidate.Sku.MaxCapacity > 0 && capacity > candidate.Sku.MaxCapacity {
		if !defaulted {
			return 0, false
		}
		// Clamp down to the highest step-aligned capacity within max.
		capacity = candidate.Sku.MaxCapacity
		if step := candidate.Sku.CapacityStep; step > 0 && capacity%step != 0 {
			capacity = (capacity / step) * step
		}
		if capacity < candidate.Sku.MinCapacity || capacity <= 0 {
			return 0, false
		}
	}

	if candidate.RemainingQuota != nil && float64(capacity) > *candidate.RemainingQuota {
		return 0, false
	}

	return capacity, true
}

func cloneDeploymentWithCapacity(candidate *azdext.AiModelDeployment, capacity int32) *azdext.AiModelDeployment {
	if candidate == nil {
		return nil
	}

	cloned := proto.Clone(candidate).(*azdext.AiModelDeployment)
	cloned.Capacity = capacity
	return cloned
}

func skuPriority(skuName string) int {
	for i, preferred := range defaultSkuPriority {
		if preferred == skuName {
			return i
		}
	}

	return len(defaultSkuPriority)
}

func isRecoverableDeploymentSelectionError(err error) bool {
	if err == nil {
		return false
	}

	return hasAiErrorReason(err,
		azdext.AiErrorReasonNoValidSkus,
		azdext.AiErrorReasonNoDeploymentMatch,
		azdext.AiErrorReasonModelNotFound,
		azdext.AiErrorReasonNoModelsMatch,
	)
}

func hasAiErrorReason(err error, reasons ...string) bool {
	if err == nil {
		return false
	}

	st, ok := status.FromError(err)
	if !ok {
		return false
	}

	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok || info.Domain != azdext.AiErrorDomain {
			continue
		}

		if slices.Contains(reasons, info.Reason) {
			return true
		}
	}

	return false
}

type modelRecoveryReason string

const (
	modelRecoveryReasonAvailability modelRecoveryReason = "availability"
	modelRecoveryReasonQuota        modelRecoveryReason = "quota"
	modelLocationSwitchWarning                          = "WARNING: If you switch locations:\n" +
		"• Your azd environment will use a new default region.\n" +
		"• Any existing Azure AI Foundry project created in your current region may fail.\n" +
		"• Quota availability varies by region and model.\n\n" +
		"Recommended options:\n" +
		"1) Select a different model in this region (safe), or\n" +
		"2) Create a new Foundry project after changing regions."
)

func (a *modelSelector) promptForAlternativeModel(
	ctx context.Context,
	originalModelName string,
) (*azdext.AiModel, error) {
	fmt.Println(output.WithErrorFormat("The model '%s' could not be found in the model catalog for your subscription in any region.\n", originalModelName))

	choices := []*azdext.SelectChoice{
		{Label: "Select a different model", Value: "select"},
		{Label: "Exit", Value: "exit"},
	}

	defaultIndex := int32(1)
	selectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         "Would you like to select a different model or exit?",
			Choices:         choices,
			SelectedIndex:   &defaultIndex,
			EnableFiltering: new(false),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for model selection choice: %w", err)
	}

	if choices[*selectResp.Value].Value == "exit" {
		return nil, nil
	}

	regionChoices := []*azdext.SelectChoice{
		{Label: fmt.Sprintf("Models available in my current region (%s)", a.azureContext.Scope.Location), Value: "region"},
		{Label: "All available models", Value: "all"},
	}

	regionResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         "Which models would you like to explore?",
			Choices:         regionChoices,
			EnableFiltering: new(false),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for region choice: %w", err)
	}

	promptReq := &azdext.PromptAiModelRequest{
		AzureContext: a.azureContext,
		Filter:       agentModelFilter(nil, nil),
		SelectOptions: &azdext.SelectOptions{
			Message: "Select a model",
		},
	}

	if regionChoices[*regionResp.Value].Value == "region" {
		promptReq.Filter = agentModelFilter([]string{a.azureContext.Scope.Location}, nil)
	}

	modelResp, err := a.azdClient.Prompt().PromptAiModel(ctx, promptReq)
	if err != nil {
		return nil, exterrors.FromPrompt(err, "failed to prompt for model selection")
	}

	return modelResp.Model, nil
}

// promptModelFromCatalog shows the model catalog list filtered to the current region,
// allowing the user to pick any available model.
func (a *modelSelector) promptModelFromCatalog(ctx context.Context) (*azdext.AiModel, error) {
	promptReq := &azdext.PromptAiModelRequest{
		AzureContext: a.azureContext,
		Filter:       agentModelFilter([]string{a.azureContext.Scope.Location}, nil),
		SelectOptions: &azdext.SelectOptions{
			Message: "Select a model",
		},
	}

	modelResp, err := a.azdClient.Prompt().PromptAiModel(ctx, promptReq)
	if err != nil {
		return nil, exterrors.FromPrompt(err, "failed to prompt for model selection")
	}

	return modelResp.Model, nil
}

// promptExistingDeployment shows the list of existing deployments in the
// Foundry project and lets the user pick one.
func (a *modelSelector) promptExistingDeployment(ctx context.Context) (*project.Deployment, error) {
	if len(a.allDeployments) == 0 {
		return nil, fmt.Errorf("no existing deployments available")
	}

	type labeledDeployment struct {
		label string
		info  *FoundryDeploymentInfo
	}

	items := make([]labeledDeployment, 0, len(a.allDeployments))
	for i := range a.allDeployments {
		d := &a.allDeployments[i]
		items = append(items, labeledDeployment{
			label: fmt.Sprintf("%s (%s v%s)", d.Name, d.ModelName, d.Version),
			info:  d,
		})
	}

	slices.SortFunc(items, func(a, b labeledDeployment) int {
		return strings.Compare(a.label, b.label)
	})

	choices := make([]*azdext.SelectChoice, len(items))
	for i, item := range items {
		choices[i] = &azdext.SelectChoice{
			Label: item.label,
			Value: item.label,
		}
	}

	defaultIdx := int32(0)
	resp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "Select an existing deployment",
			Choices:       choices,
			SelectedIndex: &defaultIdx,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("deployment selection was cancelled")
		}
		return nil, fmt.Errorf("failed to prompt for deployment selection: %w", err)
	}

	d := items[*resp.Value].info
	fmt.Printf("Using existing deployment: %s\n", d.Name)

	return &project.Deployment{
		Name: d.Name,
		Model: project.DeploymentModel{
			Name:    d.ModelName,
			Format:  d.ModelFormat,
			Version: d.Version,
		},
		Sku: project.DeploymentSku{
			Name:     d.SkuName,
			Capacity: d.SkuCapacity,
		},
	}, nil
}

func (a *modelSelector) promptForModelLocationMismatch(
	ctx context.Context,
	model *azdext.AiModel,
	currentLocation string,
	reasonMessage string,
	reasonKind modelRecoveryReason,
) (*azdext.AiModel, string, error) {
	currentModel := model
	message := reasonMessage

	for {
		if message == "" {
			message = fmt.Sprintf(
				"The model '%s' is not available in your current location '%s'.",
				currentModel.Name,
				currentLocation,
			)
		}

		fmt.Println(output.WithErrorFormat(message))

		modelChoiceLabel := fmt.Sprintf("Choose a different model in %s", currentLocation)

		choices := []*azdext.SelectChoice{
			{Label: modelChoiceLabel, Value: "model"},
			{Label: "Choose a different model (all regions)", Value: "model_all_regions"},
			{Label: fmt.Sprintf("Choose a different location for %s", currentModel.Name), Value: "location"},
			{Label: "Exit setup", Value: "exit"},
		}

		if !a.locationWarningShown {
			fmt.Println()
			fmt.Println(output.WithWarningFormat(modelLocationSwitchWarning))
			a.locationWarningShown = true
			fmt.Println()
		}

		defaultIndex := int32(3)
		selectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:         "What would you like to do?",
				Choices:         choices,
				SelectedIndex:   &defaultIndex,
				EnableFiltering: new(false),
			},
		})
		if err != nil {
			return nil, "", fmt.Errorf("failed to prompt for action choice: %w", err)
		}

		selectedChoice := choices[*selectResp.Value].Value

		if selectedChoice == "exit" {
			return nil, "", nil
		}

		if selectedChoice == "location" {
			allowedLocations, err := supportedModelLocations(ctx, currentModel.Locations)
			if err != nil {
				if isNoSupportedLocationsError(err) {
					message = fmt.Sprintf(
						"Model '%s' is not available in any region supported for hosted agents.",
						currentModel.Name,
					)
					continue
				}
				return nil, "", err
			}
			locationResp, err := a.azdClient.Prompt().PromptAiModelLocationWithQuota(ctx,
				&azdext.PromptAiModelLocationWithQuotaRequest{
					AzureContext:     a.azureContext,
					ModelName:        currentModel.Name,
					AllowedLocations: allowedLocations,
					Quota: &azdext.QuotaCheckOptions{
						MinRemainingCapacity: 1,
					},
					SelectOptions: &azdext.SelectOptions{
						Message: fmt.Sprintf("Select a location for model '%s'", currentModel.Name),
					},
				},
			)
			if err != nil {
				if hasAiErrorReason(err, azdext.AiErrorReasonNoLocationsWithQuota) {
					message = fmt.Sprintf("No locations have sufficient quota for model '%s'.", currentModel.Name)
					continue
				}

				return nil, "", exterrors.FromPrompt(err, "failed to prompt for location selection")
			}

			selectedLocation := locationResp.Location.Name
			if err := a.updateEnvLocation(ctx, selectedLocation); err != nil {
				return nil, "", err
			}

			return currentModel, selectedLocation, nil
		}

		if selectedChoice == "model_all_regions" {
			modelResp, err := a.azdClient.Prompt().PromptAiModel(ctx, &azdext.PromptAiModelRequest{
				AzureContext: a.azureContext,
				Filter:       agentModelFilter(nil, []string{currentModel.Name}),
				Quota: &azdext.QuotaCheckOptions{
					MinRemainingCapacity: 1,
				},
				SelectOptions: &azdext.SelectOptions{
					Message: "Select a model from all regions",
				},
			})
			if err != nil {
				if hasAiErrorReason(err, azdext.AiErrorReasonNoModelsMatch) {
					message = "No alternative models were found across all regions."
					continue
				}

				return nil, "", exterrors.FromPrompt(err, "failed to prompt for model selection across all regions")
			}

			selectedModel := modelResp.Model
			allowedLocations, err := supportedModelLocations(ctx, selectedModel.Locations)
			if err != nil {
				if isNoSupportedLocationsError(err) {
					currentModel = selectedModel
					message = fmt.Sprintf(
						"Model '%s' is not available in any region supported for hosted agents.",
						selectedModel.Name,
					)
					continue
				}
				return nil, "", err
			}
			locationResp, err := a.azdClient.Prompt().PromptAiModelLocationWithQuota(ctx,
				&azdext.PromptAiModelLocationWithQuotaRequest{
					AzureContext:     a.azureContext,
					ModelName:        selectedModel.Name,
					AllowedLocations: allowedLocations,
					Quota: &azdext.QuotaCheckOptions{
						MinRemainingCapacity: 1,
					},
					SelectOptions: &azdext.SelectOptions{
						Message: fmt.Sprintf("Select a location for model '%s'", selectedModel.Name),
					},
				},
			)
			if err != nil {
				if hasAiErrorReason(err, azdext.AiErrorReasonNoLocationsWithQuota) {
					currentModel = selectedModel
					message = fmt.Sprintf("No locations have sufficient quota for model '%s'.", selectedModel.Name)
					continue
				}

				return nil, "", exterrors.FromPrompt(err, "failed to prompt for location selection")
			}

			selectedLocation := locationResp.Location.Name
			if err := a.updateEnvLocation(ctx, selectedLocation); err != nil {
				return nil, "", err
			}

			return selectedModel, selectedLocation, nil
		}

		promptReq := &azdext.PromptAiModelRequest{
			AzureContext: a.azureContext,
			Filter:       agentModelFilter([]string{currentLocation}, nil),
			SelectOptions: &azdext.SelectOptions{
				Message: fmt.Sprintf("Select a model available in '%s'", currentLocation),
			},
			Quota: &azdext.QuotaCheckOptions{
				MinRemainingCapacity: 1,
			},
		}

		modelResp, err := a.azdClient.Prompt().PromptAiModel(ctx, promptReq)
		if err != nil {
			if hasAiErrorReason(err, azdext.AiErrorReasonNoModelsMatch) {
				message = fmt.Sprintf("No models are available in your current location '%s'.", currentLocation)
				continue
			}

			return nil, "", exterrors.FromPrompt(err, "failed to prompt for model selection")
		}

		return modelResp.Model, currentLocation, nil
	}
}

// envValueSetter writes a single key-value pair to the azd environment.
type envValueSetter func(ctx context.Context, key, value string) error

// persistFirstDeploymentName persists the first deployment's name as
// AZURE_AI_MODEL_DEPLOYMENT_NAME so templates and agent code can reference it.
// It is a no-op when the deployments slice is empty.
func persistFirstDeploymentName(
	ctx context.Context,
	setEnv envValueSetter,
	deployments []project.Deployment,
) error {
	if len(deployments) == 0 {
		return nil
	}

	return setEnv(ctx, "AZURE_AI_MODEL_DEPLOYMENT_NAME", deployments[0].Name)
}
