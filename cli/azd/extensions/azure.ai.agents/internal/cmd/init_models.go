// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/registry_api"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
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
		return fmt.Errorf("failed to load the model catalog: %w", err)
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

	if a.azureContext == nil {
		a.azureContext = &azdext.AzureContext{}
	}
	if a.azureContext.Scope == nil {
		a.azureContext.Scope = &azdext.AzureScope{}
	}
	a.azureContext.Scope.Location = selectedLocation

	_, err = a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: envResponse.Environment.Name,
		Key:     "AZURE_LOCATION",
		Value:   a.azureContext.Scope.Location,
	})
	if err != nil {
		return fmt.Errorf("failed to update AZURE_LOCATION in azd environment: %w", err)
	}

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

func (a *InitAction) setEnvVar(ctx context.Context, key, value string) error {
	_, err := a.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: a.environment.Name,
		Key:     key,
		Value:   value,
	})
	if err != nil {
		return fmt.Errorf("failed to set environment variable %s=%s: %w", key, value, err)
	}

	fmt.Printf("Set environment variable: %s=%s\n", key, value)
	return nil
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

		deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(subscription, a.credential, azure.NewArmClientOptions())
		if err != nil {
			return nil, fmt.Errorf("failed to create deployments client: %w", err)
		}

		pager := deploymentsClient.NewListPager(resourceGroup, accountName, nil)
		var deployments []*armcognitiveservices.Deployment
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list deployments: %w", err)
			}
			deployments = append(deployments, page.Value...)
		}

		matchingDeployments := make(map[string]*armcognitiveservices.Deployment)
		for _, deployment := range deployments {
			if deployment.Name == nil ||
				deployment.Properties == nil || deployment.Properties.Model == nil ||
				deployment.Properties.Model.Name == nil || deployment.Properties.Model.Format == nil ||
				deployment.Properties.Model.Version == nil ||
				deployment.SKU == nil || deployment.SKU.Name == nil || deployment.SKU.Capacity == nil {
				continue
			}

			if *deployment.Properties.Model.Name == model.Id {
				matchingDeployments[*deployment.Name] = deployment
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
							Format:  *deployment.Properties.Model.Format,
							Version: *deployment.Properties.Model.Version,
						},
						Sku: project.DeploymentSku{
							Name:     *deployment.SKU.Name,
							Capacity: int(*deployment.SKU.Capacity),
						},
					}, nil
				}
			}
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
		return a.resolveModelDeploymentNoPrompt(ctx, model, currentLocation)
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
			return nil, fmt.Errorf("failed to prompt for model deployment: %w", err)
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

func (a *InitAction) resolveModelDeploymentNoPrompt(
	ctx context.Context,
	model *azdext.AiModel,
	location string,
) (*azdext.AiModelDeployment, error) {
	resolveResp, err := a.azdClient.Ai().ResolveModelDeployments(ctx, &azdext.ResolveModelDeploymentsRequest{
		AzureContext: a.azureContext,
		ModelName:    model.Name,
		Options: &azdext.AiModelDeploymentOptions{
			Locations: []string{location},
		},
		Quota: &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve model deployment: %w", err)
	}

	if len(resolveResp.Deployments) == 0 {
		return nil, fmt.Errorf("no deployment candidates found for model '%s' in location '%s'", model.Name, location)
	}

	orderedCandidates := slices.Clone(resolveResp.Deployments)
	defaultVersions := make(map[string]struct{}, len(model.Versions))
	for _, version := range model.Versions {
		if version.IsDefault {
			defaultVersions[version.Version] = struct{}{}
		}
	}

	slices.SortFunc(orderedCandidates, func(a, b *azdext.AiModelDeployment) int {
		_, aDefault := defaultVersions[a.Version]
		_, bDefault := defaultVersions[b.Version]
		if aDefault != bDefault {
			if aDefault {
				return -1
			}
			return 1
		}

		aSkuPriority := skuPriority(a.Sku.Name)
		bSkuPriority := skuPriority(b.Sku.Name)
		if aSkuPriority != bSkuPriority {
			if aSkuPriority < bSkuPriority {
				return -1
			}
			return 1
		}

		if cmp := strings.Compare(a.Version, b.Version); cmp != 0 {
			return cmp
		}

		if cmp := strings.Compare(a.Sku.Name, b.Sku.Name); cmp != 0 {
			return cmp
		}

		return strings.Compare(a.Sku.UsageName, b.Sku.UsageName)
	})

	for _, candidate := range orderedCandidates {
		capacity, ok := resolveNoPromptCapacity(candidate)
		if !ok {
			continue
		}

		return cloneDeploymentWithCapacity(candidate, capacity), nil
	}

	return nil, fmt.Errorf("no deployment candidates found for model '%s' with a valid non-interactive capacity", model.Name)
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
		"• Your AZD environment will use a new default region.\n" +
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
			EnableFiltering: to.Ptr(false),
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
			EnableFiltering: to.Ptr(false),
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
		return nil, fmt.Errorf("failed to prompt for model selection: %w", err)
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
				EnableFiltering: to.Ptr(false),
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

				return nil, "", fmt.Errorf("failed to prompt for location selection: %w", err)
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

				return nil, "", fmt.Errorf("failed to prompt for model selection across all regions: %w", err)
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

				return nil, "", fmt.Errorf("failed to prompt for location selection: %w", err)
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

			return nil, "", fmt.Errorf("failed to prompt for model selection: %w", err)
		}

		return modelResp.Model, currentLocation, nil
	}
}

func (a *InitAction) ProcessModels(ctx context.Context, manifest *agent_yaml.AgentManifest) (*agent_yaml.AgentManifest, []project.Deployment, error) {
	templateBytes, err := yaml.Marshal(manifest.Template)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal agent template to YAML: %w", err)
	}

	var templateDict map[string]interface{}
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
