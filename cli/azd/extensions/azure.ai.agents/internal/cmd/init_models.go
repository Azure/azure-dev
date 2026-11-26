// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/inittelemetry"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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
const defaultDeploymentCapacity int32 = 250

type agentModelConfigOptions struct {
	AzdClient            *azdext.AzdClient
	Credential           azcore.TokenCredential
	AzureContext         *azdext.AzureContext
	Env                  *azdext.Environment
	Flags                *initFlags
	SelectedProject      *FoundryProjectInfo
	DeferredAzureContext bool
	AllowSkip            bool
	loadDeployments      foundryDeploymentsLoader
}

func validateModelDeploymentTarget(flags *initFlags, selectedProject *FoundryProjectInfo) error {
	if flags == nil || strings.TrimSpace(flags.modelDeployment) == "" || selectedProject != nil {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"the selected new project cannot reuse an existing model deployment",
		"select an existing project containing that deployment, or use --model to deploy a new model",
	)
}

func deploymentFromFoundryInfo(d *FoundryDeploymentInfo) *project.Deployment {
	if d == nil {
		return nil
	}
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
	}
}

func deploymentFromAiModelDeployment(d *azdext.AiModelDeployment) *project.Deployment {
	if d == nil {
		return nil
	}
	return &project.Deployment{
		Name: d.ModelName,
		Model: project.DeploymentModel{
			Name:    d.ModelName,
			Format:  d.Format,
			Version: d.Version,
		},
		Sku: project.DeploymentSku{
			Name:     d.GetSku().GetName(),
			Capacity: int(d.Capacity),
		},
	}
}

func promptNewOrSkipModelConfig(ctx context.Context, azdClient *azdext.AzdClient) (bool, error) {
	choices := []*azdext.SelectChoice{
		{Label: "Deploy a new model from the catalog", Value: "new"},
		{Label: "Skip model configuration", Value: "skip"},
	}
	resp, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "How would you like to configure model(s) for your agent?",
			Choices:       choices,
			SelectedIndex: new(int32(0)),
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return false, exterrors.Cancelled("model configuration choice was cancelled")
		}
		return false, exterrors.FromPrompt(err, "failed to prompt for model configuration choice")
	}
	idx := int(*resp.Value)
	if idx < 0 || idx >= len(choices) {
		return false, exterrors.Internal(
			exterrors.CodePromptFailed,
			"model configuration prompt returned an invalid index",
		)
	}
	return choices[idx].Value == "skip", nil
}

// configureAgentModelDeployment unifies model-deployment selection across hosted
// and prompt agent init flows, returning the resolved deployment and whether it
// must be provisioned (true for new catalog models, false when reusing existing
// or when skipped/deferred).
func configureAgentModelDeployment(
	ctx context.Context,
	opts agentModelConfigOptions,
) (*project.Deployment, bool, error) {
	var flags initFlags
	if opts.Flags != nil {
		flags = *opts.Flags
	}

	if err := validateModelDeploymentTarget(&flags, opts.SelectedProject); err != nil {
		return nil, false, err
	}

	if opts.DeferredAzureContext {
		if strings.TrimSpace(flags.model) != "" {
			fmt.Printf("%s", output.WithWarningFormat(
				"Model configuration was deferred because Azure environment values are missing.\n",
			))
			fmt.Println(output.WithGrayFormat(
				"Set the missing values, then re-run init with your model options " +
					"or configure deployments in azure.yaml.",
			))
		}
		return nil, false, nil
	}

	if modelDeploymentFlag := strings.TrimSpace(flags.modelDeployment); modelDeploymentFlag != "" {
		d, _, err := selectModelDeployment(
			ctx,
			opts.AzdClient,
			opts.Credential,
			*opts.SelectedProject,
			modelDeploymentFlag,
			"",
			false,
			opts.loadDeployments,
		)
		if err != nil {
			return nil, false, err
		}
		return deploymentFromFoundryInfo(d), false, nil
	}

	if modelFlag := strings.TrimSpace(flags.model); modelFlag != "" {
		if opts.AzureContext.Scope.Location == "" {
			if err := ensureLocation(ctx, opts.AzdClient, opts.AzureContext, opts.Env.Name); err != nil {
				return nil, false, err
			}
		}
		selector := &modelSelector{
			azdClient:     opts.AzdClient,
			azureContext:  opts.AzureContext,
			environment:   opts.Env,
			flags:         opts.Flags,
			locationFixed: opts.SelectedProject != nil,
		}
		selectedModel, err := selector.resolveExplicitModel(ctx, modelFlag)
		if err != nil {
			return nil, false, err
		}
		if flags.noPrompt {
			fmt.Println(
				"No prompt mode enabled, automatically selecting a model deployment " +
					"based on availability and quota...",
			)
		}
		modelDetails, err := selector.resolveOrPromptModelDeployment(ctx, selectedModel)
		if err != nil {
			return nil, false, err
		}
		return deploymentFromAiModelDeployment(modelDetails), true, nil
	}

	if flags.noPrompt {
		if opts.AllowSkip {
			return nil, false, nil
		}
		return nil, false, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--model or --model-deployment is required in non-interactive mode for prompt agents",
			"pass --model <model-name> or --model-deployment <deployment-name> on the command line",
		)
	}

	if opts.SelectedProject != nil {
		d, action, err := selectModelDeployment(
			ctx,
			opts.AzdClient,
			opts.Credential,
			*opts.SelectedProject,
			"",
			"",
			opts.AllowSkip,
			opts.loadDeployments,
		)
		if err != nil {
			return nil, false, err
		}
		switch action {
		case modelDeploymentSelected:
			return deploymentFromFoundryInfo(d), false, nil
		case modelDeploymentSkipped:
			return nil, false, nil
		case modelDeploymentEmpty:
			if opts.AllowSkip {
				skip, err := promptNewOrSkipModelConfig(ctx, opts.AzdClient)
				if err != nil || skip {
					return nil, false, err
				}
			}
		case modelDeploymentCreateNew:
			// Proceed to catalog selection below.
		}
	} else if opts.AllowSkip {
		skip, err := promptNewOrSkipModelConfig(ctx, opts.AzdClient)
		if err != nil || skip {
			return nil, false, err
		}
	}

	if opts.AzureContext.Scope.Location == "" {
		if err := ensureLocation(ctx, opts.AzdClient, opts.AzureContext, opts.Env.Name); err != nil {
			return nil, false, err
		}
	}
	selectedModel, err := selectNewModel(ctx, opts.AzdClient, opts.AzureContext, "")
	if err != nil {
		return nil, false, err
	}
	selector := &modelSelector{
		azdClient:     opts.AzdClient,
		azureContext:  opts.AzureContext,
		environment:   opts.Env,
		flags:         opts.Flags,
		modelCatalog:  map[string]*azdext.AiModel{selectedModel.Name: selectedModel},
		locationFixed: opts.SelectedProject != nil,
	}
	modelDetails, err := selector.resolveOrPromptModelDeployment(ctx, selectedModel)
	if err != nil {
		return nil, false, err
	}
	return deploymentFromAiModelDeployment(modelDetails), true, nil
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

func (a *modelSelector) lookupCatalogModel(modelName string) (*azdext.AiModel, bool) {
	if model, ok := a.modelCatalog[modelName]; ok {
		return model, true
	}
	for name, model := range a.modelCatalog {
		if strings.EqualFold(name, modelName) {
			return model, true
		}
	}
	return nil, false
}

func modelAvailableInLocation(model *azdext.AiModel, location string) bool {
	if model == nil {
		return false
	}
	normalized := normalizeLocationName(location)
	if normalized == "" {
		return false
	}
	return slices.ContainsFunc(model.Locations, func(candidate string) bool {
		return normalizeLocationName(candidate) == normalized
	})
}

func (a *modelSelector) resolveExplicitModel(
	ctx context.Context,
	modelName string,
) (*azdext.AiModel, error) {
	defer inittelemetry.Start(ctx, inittelemetry.ModelSelection)()

	if err := a.loadAiCatalog(ctx); err != nil {
		return nil, err
	}

	noPrompt := a.flags != nil && a.flags.noPrompt
	model, exists := a.lookupCatalogModel(modelName)
	if !exists {
		if noPrompt {
			return nil, exterrors.Dependency(
				exterrors.CodeModelResolutionFailed,
				fmt.Sprintf(
					"model %q was not found in the AI model catalog for your subscription",
					modelName,
				),
				fmt.Sprintf(
					"pass --model with a valid catalog model name (e.g. --model %s), "+
						"or run interactively to browse available models",
					defaultAgentModel,
				),
			)
		}
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
	if !modelAvailableInLocation(model, currentLocation) {
		if noPrompt {
			return nil, exterrors.Dependency(
				exterrors.CodeModelResolutionFailed,
				fmt.Sprintf("model %q is not available in location %q", model.Name, currentLocation),
				"choose a model available in the target region, or set AZURE_LOCATION / "+
					"AZURE_AI_DEPLOYMENTS_LOCATION to a region where this model is available",
			)
		}
		resolvedModel, _, err := a.promptForModelLocationMismatch(
			ctx,
			model,
			currentLocation,
			fmt.Sprintf(
				"The model '%s' is not available in your current location '%s'.",
				model.Name,
				currentLocation,
			),
			modelRecoveryReasonAvailability,
		)
		if err != nil {
			return nil, err
		}
		if resolvedModel == nil {
			return nil, fmt.Errorf("model unavailable in current location and no alternative selected, exiting")
		}
		model = resolvedModel
	}

	return model, nil
}

func (a *modelSelector) resolveOrPromptModelDeployment(
	ctx context.Context,
	model *azdext.AiModel,
) (*azdext.AiModelDeployment, error) {
	noPrompt := a.flags != nil && a.flags.noPrompt
	currentLocation := a.azureContext.Scope.Location

	for {
		if noPrompt {
			return resolveModelDeployment(ctx, a.azdClient, a.azureContext, model, currentLocation)
		}

		deployments, err := resolveModelDeployments(ctx, a.azdClient, a.azureContext, model, currentLocation)
		if err == nil {
			if candidate := selectBestModelDeploymentCandidate(model, deployments); candidate != nil {
				return candidate, nil
			}
		} else if !isRecoverableDeploymentSelectionError(err) {
			return nil, exterrors.FromAiService(err, exterrors.CodeModelResolutionFailed)
		}

		deploymentResp, promptErr := a.promptAiDeploymentWithFallback(ctx, model.Name, currentLocation)
		if promptErr == nil {
			return deploymentResp.Deployment, nil
		}

		if !isRecoverableDeploymentSelectionError(promptErr) {
			return nil, exterrors.FromPrompt(promptErr, "failed to prompt for model deployment")
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

func (a *modelSelector) promptAiDeploymentWithFallback(
	ctx context.Context,
	modelName string,
	location string,
) (*azdext.PromptAiDeploymentResponse, error) {
	if fallback, scopeCandidate, ok := a.resolveFallbackPromptDeployment(ctx, modelName, location); ok {
		retryOpts := &azdext.AiModelDeploymentOptions{
			Locations: []string{location},
			Capacity:  new(fallback.GetCapacity()),
		}
		if scopeCandidate {
			if version := fallback.GetVersion(); version != "" {
				retryOpts.Versions = []string{version}
			}
			if skuName := fallback.GetSku().GetName(); skuName != "" {
				retryOpts.Skus = []string{skuName}
			}
		}
		deploymentResp, err := a.azdClient.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
			AzureContext: a.azureContext,
			ModelName:    modelName,
			Options:      retryOpts,
			Quota: &azdext.QuotaCheckOptions{
				MinRemainingCapacity: 1,
			},
		})
		if err == nil || !hasAiErrorReason(err,
			azdext.AiErrorReasonNoValidSkus,
			azdext.AiErrorReasonNoDeploymentMatch,
		) {
			return deploymentResp, err
		}
	} else {
		deploymentResp, err := a.azdClient.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
			AzureContext: a.azureContext,
			ModelName:    modelName,
			Options: &azdext.AiModelDeploymentOptions{
				Locations: []string{location},
				Capacity:  new(defaultDeploymentCapacity),
			},
			Quota: &azdext.QuotaCheckOptions{
				MinRemainingCapacity: 1,
			},
		})
		if err == nil || !hasAiErrorReason(err,
			azdext.AiErrorReasonNoValidSkus,
			azdext.AiErrorReasonNoDeploymentMatch,
		) {
			return deploymentResp, err
		}
	}

	return a.azdClient.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
		AzureContext: a.azureContext,
		ModelName:    modelName,
		Options: &azdext.AiModelDeploymentOptions{
			Locations: []string{location},
		},
		Quota: &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		},
	})
}

func (a *modelSelector) resolveFallbackPromptDeployment(
	ctx context.Context,
	modelName string,
	location string,
) (*azdext.AiModelDeployment, bool, bool) {
	resolveResp, err := a.azdClient.Ai().ResolveModelDeployments(ctx, &azdext.ResolveModelDeploymentsRequest{
		AzureContext: a.azureContext,
		ModelName:    modelName,
		Options: &azdext.AiModelDeploymentOptions{
			Locations: []string{location},
		},
		Quota: &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		},
	})
	if err != nil || len(resolveResp.GetDeployments()) == 0 {
		return nil, false, false
	}

	candidates := make([]*azdext.AiModelDeployment, 0, len(resolveResp.GetDeployments()))
	for _, candidate := range resolveResp.GetDeployments() {
		if candidate == nil {
			continue
		}
		candidates = append(candidates, cloneDeploymentWithCapacity(candidate, 0))
	}

	var model *azdext.AiModel
	if a.modelCatalog != nil {
		model = a.modelCatalog[modelName]
	}
	if model == nil {
		model = &azdext.AiModel{Name: modelName}
	}
	best := selectBestModelDeploymentCandidate(model, candidates)
	if best == nil || best.GetCapacity() <= 0 || best.GetCapacity() >= defaultDeploymentCapacity {
		return nil, false, false
	}
	scopeCandidate := needsAgentCandidateScoping(candidates, best.GetCapacity())
	return best, scopeCandidate, true
}

func needsAgentCandidateScoping(
	candidates []*azdext.AiModelDeployment,
	targetCapacity int32,
) bool {
	for _, candidate := range candidates {
		if candidate == nil || candidate.GetSku() == nil {
			continue
		}
		if !capacityValidForSku(candidate.GetSku(), targetCapacity) {
			return true
		}
		if capVal, ok := resolveNoPromptCapacity(candidate); ok && capVal > targetCapacity {
			return true
		}
	}
	return false
}

func skuCapacityBaseline(sku *azdext.AiModelSku) int32 {
	if minCapacity := sku.GetMinCapacity(); minCapacity > 0 {
		return minCapacity
	}
	if capacityStep := sku.GetCapacityStep(); capacityStep > 0 {
		return capacityStep
	}
	return 1
}

func capacityValidForSku(sku *azdext.AiModelSku, capacity int32) bool {
	if capacity <= 0 {
		return false
	}
	if minCapacity := sku.GetMinCapacity(); minCapacity > 0 && capacity < minCapacity {
		return false
	}
	if maxCapacity := sku.GetMaxCapacity(); maxCapacity > 0 && capacity > maxCapacity {
		return false
	}
	if step := sku.GetCapacityStep(); step > 0 {
		baseline := skuCapacityBaseline(sku)
		if capacity < baseline || (capacity-baseline)%step != 0 {
			return false
		}
	}
	return true
}

func clampCapacityDown(sku *azdext.AiModelSku, upperBound int32) (int32, bool) {
	if upperBound <= 0 {
		return 0, false
	}
	if maxCapacity := sku.GetMaxCapacity(); maxCapacity > 0 && upperBound > maxCapacity {
		upperBound = maxCapacity
	}
	baseline := skuCapacityBaseline(sku)
	if upperBound < baseline {
		return 0, false
	}
	if step := sku.GetCapacityStep(); step > 0 {
		upperBound -= (upperBound - baseline) % step
	}
	if upperBound < sku.GetMinCapacity() || upperBound <= 0 {
		return 0, false
	}
	return upperBound, true
}

func resolveNoPromptCapacity(candidate *azdext.AiModelDeployment) (int32, bool) {
	sku := candidate.GetSku()
	capacity := candidate.Capacity
	defaulted := capacity <= 0
	if defaulted {
		baseline := skuCapacityBaseline(sku)
		upperBound := max(baseline, defaultDeploymentCapacity)
		if maxCapacity := sku.GetMaxCapacity(); maxCapacity > 0 && upperBound > maxCapacity {
			upperBound = maxCapacity
		}
		if candidate.RemainingQuota != nil {
			quotaBound := int32(*candidate.RemainingQuota)
			if quotaBound < upperBound {
				upperBound = quotaBound
			}
		}
		clamped, ok := clampCapacityDown(sku, upperBound)
		if !ok || !capacityValidForSku(sku, clamped) {
			return 0, false
		}
		if candidate.RemainingQuota != nil && float64(clamped) > *candidate.RemainingQuota {
			return 0, false
		}
		return clamped, true
	}

	if sku.GetMinCapacity() > 0 && capacity < sku.GetMinCapacity() {
		capacity = sku.GetMinCapacity()
	}

	if step := sku.GetCapacityStep(); step > 0 {
		baseline := skuCapacityBaseline(sku)
		if capacity < baseline {
			capacity = baseline
		}
		if rem := (capacity - baseline) % step; rem != 0 {
			capacity += step - rem
		}
	}

	if sku.GetMaxCapacity() > 0 && capacity > sku.GetMaxCapacity() {
		return 0, false
	}

	if candidate.RemainingQuota != nil && float64(capacity) > *candidate.RemainingQuota {
		return 0, false
	}

	if !capacityValidForSku(sku, capacity) {
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
	fmt.Println(output.WithErrorFormat(
		"The model '%s' could not be found in the model catalog for your subscription in any region.\n",
		originalModelName,
	))

	choices := []*azdext.SelectChoice{
		{Label: "Select a different model", Value: "select"},
		{Label: "Exit", Value: "exit"},
	}

	selectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         "Would you like to select a different model or exit?",
			Choices:         choices,
			SelectedIndex:   new(int32(1)),
			EnableFiltering: new(false),
		},
	})
	if err != nil {
		return nil, exterrors.FromPrompt(err, "failed to prompt for model selection choice")
	}

	if choices[*selectResp.Value].Value == "exit" {
		return nil, nil
	}

	filter := agentModelFilter([]string{a.azureContext.Scope.Location}, nil)
	if !a.locationFixed {
		regionChoices := []*azdext.SelectChoice{
			{
				Label: fmt.Sprintf("Models available in my current region (%s)", a.azureContext.Scope.Location),
				Value: "region",
			},
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
			return nil, exterrors.FromPrompt(err, "failed to prompt for region choice")
		}

		if regionChoices[*regionResp.Value].Value != "region" {
			filter = agentModelFilter(nil, nil)
		}
	}

	promptReq := &azdext.PromptAiModelRequest{
		AzureContext: a.azureContext,
		Filter:       filter,
		SelectOptions: &azdext.SelectOptions{
			Message: "Select a model",
		},
		DefaultValue: defaultAgentModel,
	}

	modelResp, err := a.azdClient.Prompt().PromptAiModel(ctx, promptReq)
	if err != nil {
		return nil, exterrors.FromPrompt(err, "failed to prompt for model selection")
	}

	return modelResp.Model, nil
}

// promptModelFromCatalog shows the model catalog list filtered to the given region,
// defaulting to defaultAgentModel.
func (a *modelSelector) promptModelFromCatalog(
	ctx context.Context,
	location string,
) (*azdext.AiModel, error) {
	promptReq := &azdext.PromptAiModelRequest{
		AzureContext: a.azureContext,
		Filter:       agentModelFilter([]string{location}, nil),
		SelectOptions: &azdext.SelectOptions{
			Message: fmt.Sprintf("Select a model available in '%s'", location),
		},
		Quota: &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		},
		DefaultValue: defaultAgentModel,
	}

	modelResp, err := a.azdClient.Prompt().PromptAiModel(ctx, promptReq)
	if err != nil {
		return nil, err
	}

	return modelResp.Model, nil
}

func (a *modelSelector) promptForModelLocationMismatch(
	ctx context.Context,
	model *azdext.AiModel,
	currentLocation string,
	reasonMessage string,
	reasonKind modelRecoveryReason,
) (*azdext.AiModel, string, error) {
	_ = reasonKind
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

		var choices []*azdext.SelectChoice
		var exitIndex int32
		if a.locationFixed {
			choices = []*azdext.SelectChoice{
				{Label: modelChoiceLabel, Value: "model"},
				{Label: "Exit setup", Value: "exit"},
			}
			exitIndex = 1
		} else {
			choices = []*azdext.SelectChoice{
				{Label: modelChoiceLabel, Value: "model"},
				{Label: "Choose a different model (all regions)", Value: "model_all_regions"},
				{Label: fmt.Sprintf("Choose a different location for %s", currentModel.Name), Value: "location"},
				{Label: "Exit setup", Value: "exit"},
			}
			exitIndex = 3
		}

		if !a.locationFixed && !a.locationWarningShown {
			fmt.Println()
			fmt.Println(output.WithWarningFormat(modelLocationSwitchWarning))
			a.locationWarningShown = true
			fmt.Println()
		}

		selectResp, err := a.azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message:         "What would you like to do?",
				Choices:         choices,
				SelectedIndex:   new(exitIndex),
				EnableFiltering: new(false),
			},
		})
		if err != nil {
			return nil, "", exterrors.FromPrompt(err, "failed to prompt for action choice")
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
			defaultAllRegionsModel := ""
			if !strings.EqualFold(currentModel.Name, defaultAgentModel) {
				defaultAllRegionsModel = defaultAgentModel
			}
			modelResp, err := a.azdClient.Prompt().PromptAiModel(ctx, &azdext.PromptAiModelRequest{
				AzureContext: a.azureContext,
				Filter:       agentModelFilter(nil, []string{currentModel.Name}),
				Quota: &azdext.QuotaCheckOptions{
					MinRemainingCapacity: 1,
				},
				SelectOptions: &azdext.SelectOptions{
					Message: "Select a model from all regions",
				},
				DefaultValue: defaultAllRegionsModel,
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

		selectedModel, err := a.promptModelFromCatalog(ctx, currentLocation)
		if err != nil {
			if hasAiErrorReason(err, azdext.AiErrorReasonNoModelsMatch) {
				message = fmt.Sprintf("No models are available in your current location '%s'.", currentLocation)
				continue
			}

			return nil, "", exterrors.FromPrompt(err, "failed to prompt for model selection")
		}

		return selectedModel, currentLocation, nil
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
