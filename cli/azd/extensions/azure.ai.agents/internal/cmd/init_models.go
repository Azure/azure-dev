// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/registry_api"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

var defaultSkuPriority = []string{"GlobalStandard", "DataZoneStandard", "Standard"}

func (a *InitAction) loadAiCatalog(ctx context.Context) error {
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

func (a *InitAction) updateEnvLocation(ctx context.Context, selectedLocation string) error {
	envResponse, err := a.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to get current azd environment: %w", err)
	}

	_, err = a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envResponse.Environment.Name,
		Key:     "AZURE_LOCATION",
		Value:   selectedLocation,
	})
	if err != nil {
		return fmt.Errorf("failed to update AZURE_LOCATION in azd environment: %w", err)
	}

	if a.azureContext == nil {
		a.azureContext = &azdext.AzureContext{}
	}
	if a.azureContext.Scope == nil {
		a.azureContext.Scope = &azdext.AzureScope{}
	}
	a.azureContext.Scope.Location = selectedLocation

	fmt.Println(output.WithSuccessFormat("Updated AZURE_LOCATION to '%s' in your azd environment.", selectedLocation))
	return nil
}

func (a *InitAction) selectFromList(
	ctx context.Context, property string, options []string, defaultOpt string) (string, error) {

	if len(options) == 1 {
		fmt.Printf("Only one %s available: %s\n", property, options[0])
		return options[0], nil
	}

	slices.Sort(options)

	defaultStr := options[0]
	if defaultOpt != "" {
		defaultStr = defaultOpt
	}

	if a.flags.NoPrompt {
		fmt.Printf("No prompt mode enabled, selecting default %s: %s\n", property, defaultStr)
		return defaultStr, nil
	}

	choices := make([]*azdext.SelectChoice, len(options))
	defaultIndex := int32(0)
	for i, val := range options {
		choices[i] = &azdext.SelectChoice{
			Value: val,
			Label: val,
		}
		if val == defaultStr {
			defaultIndex = int32(i)
		}
	}
	resp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       fmt.Sprintf("Select %s", property),
			Choices:       choices,
			SelectedIndex: &defaultIndex,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to prompt for enum value: %w", err)
	}

	return options[*resp.Value], nil
}

func (a *InitAction) getModelDeploymentDetails(ctx context.Context, model agent_yaml.Model) (*project.Deployment, error) {
	resp, err := a.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: a.environment.Name,
		Key:     "AZURE_AI_PROJECT_ID",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get the environment variable AZURE_AI_PROJECT_ID from your azd environment: %w", err)
	}

	foundryProjectId := resp.Value
	if foundryProjectId != "" {
		parts := strings.Split(foundryProjectId, "/")
		if len(parts) < 9 {
			return nil, fmt.Errorf(
				"invalid AZURE_AI_PROJECT_ID format: expected at least 9 path segments, got %d", len(parts))
		}

		subscription := parts[2]
		resourceGroup := parts[4]
		accountName := parts[8]

		allDeployments, err := listProjectDeployments(ctx, a.credential, subscription, resourceGroup, accountName)
		if err != nil {
			return nil, fmt.Errorf("failed to list deployments: %w", err)
		}

		matchingDeployments := make(map[string]*FoundryDeploymentInfo)
		for i := range allDeployments {
			d := &allDeployments[i]
			if d.ModelName == model.Id {
				matchingDeployments[d.Name] = d
			}
		}

		if len(matchingDeployments) > 0 {
			fmt.Printf("In your Microsoft Foundry project, found %d existing model deployment(s) matching your model %s.\n", len(matchingDeployments), model.Id)

			var options []string
			for deploymentName := range matchingDeployments {
				options = append(options, deploymentName)
			}
			options = append(options, "Create new model deployment")

			selection, err := a.selectFromList(ctx, "deployment", options, options[0])
			if err != nil {
				return nil, fmt.Errorf("failed to select deployment: %w", err)
			}

			if selection != "Create new model deployment" {
				fmt.Printf("Using existing model deployment: %s\n", selection)

				if deployment, exists := matchingDeployments[selection]; exists {
					return &project.Deployment{
						Name: selection,
						Model: project.DeploymentModel{
							Name:    model.Id,
							Format:  deployment.ModelFormat,
							Version: deployment.Version,
						},
						Sku: project.DeploymentSku{
							Name:     deployment.SkuName,
							Capacity: deployment.SkuCapacity,
						},
					}, nil
				}
			}
		} else {
			color.Yellow(
				"No existing deployment for model '%s' specified in the selected agent manifest was found in your Foundry project.\n",
				model.Id,
			)

			noMatchChoices := []*azdext.SelectChoice{
				{
					Label: fmt.Sprintf("Deploy a new '%s' model to the selected Foundry project", model.Id),
					Value: "deploy_new",
				},
				{
					Label: "Use a different model already deployed in this project",
					Value: "use_different",
				},
			}

			defaultIdx := int32(0)
			noMatchResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
				Options: &azdext.SelectOptions{
					Message:       "How would you like to proceed?",
					Choices:       noMatchChoices,
					SelectedIndex: &defaultIdx,
				},
			})
			if err != nil {
				if exterrors.IsCancellation(err) {
					return nil, exterrors.Cancelled("model deployment selection was cancelled")
				}
				return nil, fmt.Errorf("failed to prompt for no-match choice: %w", err)
			}

			if noMatchChoices[*noMatchResp.Value].Value == "use_different" {
				if len(allDeployments) == 0 {
					fmt.Println("No deployments found in this project. A new deployment will be configured.")
				} else {
					// Let user pick from all deployments in the project
					deploymentOptions := make([]string, 0, len(allDeployments))
					deploymentMap := make(map[string]*FoundryDeploymentInfo)
					for i := range allDeployments {
						d := &allDeployments[i]
						label := fmt.Sprintf("%s (%s)", d.Name, d.ModelName)
						deploymentOptions = append(deploymentOptions, label)
						deploymentMap[label] = d
					}

					slices.Sort(deploymentOptions)

					selection, err := a.selectFromList(ctx, "deployment", deploymentOptions, deploymentOptions[0])
					if err != nil {
						return nil, fmt.Errorf("failed to select deployment: %w", err)
					}

					if deployment, exists := deploymentMap[selection]; exists {
						fmt.Printf("Using existing model deployment: %s\n", deployment.Name)
						return &project.Deployment{
							Name: deployment.Name,
							Model: project.DeploymentModel{
								Name:    deployment.ModelName,
								Format:  deployment.ModelFormat,
								Version: deployment.Version,
							},
							Sku: project.DeploymentSku{
								Name:     deployment.SkuName,
								Capacity: deployment.SkuCapacity,
							},
						}, nil
					}
				}
			}
			// "deploy_new" or no deployments available — fall through to deploy-new logic below
		}
	}

	modelDetails, err := a.getModelDetails(ctx, model.Id)
	if err != nil {
		return nil, fmt.Errorf("failed to get model details: %w", err)
	}

	message := fmt.Sprintf("Enter model deployment name for model '%s' (defaults to model name)", modelDetails.ModelName)

	modelDeploymentInput, err := a.azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:        message,
			IgnoreHintKeys: true,
			DefaultValue:   modelDetails.ModelName,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for text value: %w", err)
	}

	modelDeployment := modelDeploymentInput.Value

	return &project.Deployment{
		Name: modelDeployment,
		Model: project.DeploymentModel{
			Name:    modelDetails.ModelName,
			Format:  modelDetails.Format,
			Version: modelDetails.Version,
		},
		Sku: project.DeploymentSku{
			Name:     modelDetails.Sku.Name,
			Capacity: int(modelDetails.Capacity),
		},
	}, nil
}

func (a *InitAction) getModelDetails(ctx context.Context, modelName string) (*azdext.AiModelDeployment, error) {
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

	if a.flags.NoPrompt {
		fmt.Println("No prompt mode enabled, automatically selecting a model deployment based on availability and quota...")
		return resolveModelDeployment(ctx, a.azdClient, a.azureContext, model, currentLocation)
	}

	for {
		deploymentResp, err := a.azdClient.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
			AzureContext: a.azureContext,
			ModelName:    model.Name,
			Options: &azdext.AiModelDeploymentOptions{
				Locations: []string{currentLocation},
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
	if capacity <= 0 {
		capacity = max(candidate.Sku.MinCapacity, int32(1))
	}

	if candidate.Sku.CapacityStep > 0 && capacity%candidate.Sku.CapacityStep != 0 {
		step := candidate.Sku.CapacityStep
		capacity = ((capacity + step - 1) / step) * step
	}

	if candidate.Sku.MinCapacity > 0 && capacity < candidate.Sku.MinCapacity {
		capacity = candidate.Sku.MinCapacity
	}
	if candidate.Sku.MaxCapacity > 0 && capacity > candidate.Sku.MaxCapacity {
		return 0, false
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

func (a *InitAction) promptForAlternativeModel(
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
		SelectOptions: &azdext.SelectOptions{
			Message: "Select a model",
		},
	}

	if regionChoices[*regionResp.Value].Value == "region" {
		promptReq.Filter = &azdext.AiModelFilterOptions{
			Locations: []string{a.azureContext.Scope.Location},
		}
	}

	modelResp, err := a.azdClient.Prompt().PromptAiModel(ctx, promptReq)
	if err != nil {
		return nil, exterrors.FromPrompt(err, "failed to prompt for model selection")
	}

	return modelResp.Model, nil
}

func (a *InitAction) promptForModelLocationMismatch(
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
			locationResp, err := a.azdClient.Prompt().PromptAiModelLocationWithQuota(ctx,
				&azdext.PromptAiModelLocationWithQuotaRequest{
					AzureContext:     a.azureContext,
					ModelName:        currentModel.Name,
					AllowedLocations: currentModel.Locations,
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
				Filter: &azdext.AiModelFilterOptions{
					ExcludeModelNames: []string{currentModel.Name},
				},
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
			locationResp, err := a.azdClient.Prompt().PromptAiModelLocationWithQuota(ctx,
				&azdext.PromptAiModelLocationWithQuotaRequest{
					AzureContext:     a.azureContext,
					ModelName:        selectedModel.Name,
					AllowedLocations: selectedModel.Locations,
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
			Filter: &azdext.AiModelFilterOptions{
				Locations: []string{currentLocation},
			},
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

func (a *InitAction) ProcessModels(ctx context.Context, manifest *agent_yaml.AgentManifest) (*agent_yaml.AgentManifest, []project.Deployment, error) {
	templateBytes, err := yaml.Marshal(manifest.Template)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal agent template to YAML: %w", err)
	}

	var templateDict map[string]any
	if err := yaml.Unmarshal(templateBytes, &templateDict); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal agent template from YAML: %w", err)
	}

	dictJsonBytes, err := yaml.Marshal(templateDict)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal templateDict to YAML: %w", err)
	}

	var agentDef agent_yaml.AgentDefinition
	if err := yaml.Unmarshal(dictJsonBytes, &agentDef); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal YAML to AgentDefinition: %w", err)
	}

	deploymentDetails := []project.Deployment{}
	paramValues := registry_api.ParameterValues{}
	switch agentDef.Kind {
	case agent_yaml.AgentKindPrompt:
		agentDef := manifest.Template.(agent_yaml.PromptAgent)

		modelDeployment, err := a.getModelDeploymentDetails(ctx, agentDef.Model)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get model deployment details: %w", err)
		}
		deploymentDetails = append(deploymentDetails, *modelDeployment)
		paramValues["deploymentName"] = modelDeployment.Name
	case agent_yaml.AgentKindHosted:
		for _, resource := range manifest.Resources {
			resourceBytes, err := yaml.Marshal(resource)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal resource to YAML: %w", err)
			}

			var resourceDef agent_yaml.Resource
			if err := yaml.Unmarshal(resourceBytes, &resourceDef); err != nil {
				return nil, nil, fmt.Errorf("failed to unmarshal YAML to Resource: %w", err)
			}

			if resourceDef.Kind == agent_yaml.ResourceKindModel {
				resource := resource.(agent_yaml.ModelResource)
				model := agent_yaml.Model{Id: resource.Id}
				modelDeployment, err := a.getModelDeploymentDetails(ctx, model)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to get model deployment details: %w", err)
				}
				deploymentDetails = append(deploymentDetails, *modelDeployment)
				paramValues[resource.Name] = modelDeployment.Name
			}
		}
	}

	updatedManifest, err := registry_api.InjectParameterValuesIntoManifest(manifest, paramValues)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to inject deployment names into manifest: %w", err)
	}

	fmt.Println("Model deployment details processed and injected into agent definition. Deployment details can also be found in the JSON formatted AI_PROJECT_DEPLOYMENTS environment variable.")

	return updatedManifest, deploymentDetails, nil
}
