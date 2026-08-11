// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type deploymentMutation string

const (
	deploymentCreated   deploymentMutation = "created"
	deploymentReplaced  deploymentMutation = "replaced"
	deploymentUnchanged deploymentMutation = "unchanged"
)

type selectedDeployment struct {
	Deployment synthesis.Deployment
	Location   string
}

type deploymentSelectionOptions struct {
	Version  string
	SKU      string
	Capacity int32
	Location string
}

type deploymentItem struct {
	Raw        map[string]any
	Resolved   map[string]any
	Referenced bool
	Index      int
}

func splitModelReference(raw string) (format, name string) {
	raw = strings.TrimSpace(raw)
	if slash := strings.IndexByte(raw, '/'); slash > 0 && slash < len(raw)-1 {
		return raw[:slash], raw[slash+1:]
	}
	return "OpenAI", raw
}

func chooseDeploymentName(requested string, modelName string) string {
	if strings.TrimSpace(requested) != "" {
		return strings.TrimSpace(requested)
	}
	return modelName
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func selectModelDeployment(
	ctx context.Context,
	client *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	model delegatedModel,
	selection deploymentSelectionOptions,
	noPrompt bool,
) (*selectedDeployment, error) {
	modelFormat, modelName := splitModelReference(model.Name)
	if modelName == "" {
		return nil, contractValidationError("model.name is required")
	}

	locations, err := deploymentLocations(
		model.AllowedLocations,
		azureContextLocation(azureContext),
		selection.Location,
	)
	if err != nil {
		return nil, err
	}
	if len(model.RequiredCapabilities) > 0 || len(model.ExcludedModelNames) > 0 {
		catalog, catalogErr := client.Ai().ListModels(ctx, &azdext.ListModelsRequest{
			AzureContext: azureContext,
			Filter: &azdext.AiModelFilterOptions{
				Locations:         locations,
				Capabilities:      model.RequiredCapabilities,
				ExcludeModelNames: model.ExcludedModelNames,
			},
		})
		if catalogErr != nil {
			return nil, fmt.Errorf("filter model catalog: %w", catalogErr)
		}
		found := false
		for _, candidate := range catalog.GetModels() {
			if candidate != nil && strings.EqualFold(candidate.GetName(), modelName) {
				found = true
				for _, required := range model.RequiredCapabilities {
					if !slices.Contains(candidate.GetCapabilities(), required) {
						found = false
						break
					}
				}
				for _, excluded := range model.ExcludedModelNames {
					if strings.EqualFold(excluded, candidate.GetName()) {
						found = false
						break
					}
				}
				break
			}
		}
		if !found {
			return nil, exterrors.Validation(
				"model_not_available",
				fmt.Sprintf("model %q does not satisfy the requested capability or exclusion filters", modelName),
				"choose a compatible model or remove the consumer-specific filter",
			)
		}
	}

	options := &azdext.AiModelDeploymentOptions{
		Locations: locations,
		Versions:  nonEmptyStringSlice(selection.Version),
		Skus:      nonEmptyStringSlice(selection.SKU),
	}
	if selection.Capacity > 0 {
		options.Capacity = new(selection.Capacity)
	}
	candidates, err := resolveDeploymentCandidates(
		ctx, client, azureContext, modelName, options,
	)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, exterrors.Validation(
			"model_deployment_unavailable",
			fmt.Sprintf("no deployable version or SKU was found for model %q", modelName),
			"choose a model and location supported by your subscription",
		)
	}
	slices.SortFunc(candidates, func(left, right *azdext.AiModelDeployment) int {
		leftKey := deploymentCandidateKey(left)
		rightKey := deploymentCandidateKey(right)
		return strings.Compare(leftKey, rightKey)
	})
	if noPrompt && len(candidates) > 1 {
		return nil, exterrors.Validation(
			"model_deployment_ambiguous",
			fmt.Sprintf("more than one deployment choice is valid for %q", modelName),
			"specify the model version, SKU, capacity, or location before retrying",
		)
	}
	candidate := candidates[0]
	if !noPrompt && len(candidates) > 1 {
		candidate, err = promptForDeploymentCandidate(ctx, client, candidates)
		if err != nil {
			return nil, err
		}
	}
	if candidate == nil || candidate.GetSku() == nil {
		return nil, exterrors.Validation(
			"model_deployment_unavailable",
			fmt.Sprintf("model %q has no usable deployment SKU", modelName),
			"choose a different model",
		)
	}
	capacity := candidate.GetCapacity()
	if capacity <= 0 {
		capacity = candidate.GetSku().GetDefaultCapacity()
	}
	if capacity <= 0 {
		capacity = 1
	}
	location := candidate.GetLocation()
	if location == "" && len(locations) == 1 {
		location = locations[0]
	}
	return &selectedDeployment{
		Deployment: synthesis.Deployment{
			Name: chooseDeploymentName(model.DeploymentName, candidate.GetModelName()),
			Model: synthesis.DeploymentModel{
				Format:  firstNonEmpty(candidate.GetFormat(), modelFormat),
				Name:    firstNonEmpty(candidate.GetModelName(), modelName),
				Version: candidate.GetVersion(),
			},
			Sku: synthesis.DeploymentSku{
				Name:     candidate.GetSku().GetName(),
				Capacity: int(capacity),
			},
		},
		Location: location,
	}, nil
}

func deploymentLocations(
	allowed []string,
	projectLocation string,
	explicitLocation string,
) ([]string, error) {
	locations, err := normalizeLocations(allowed)
	if err != nil {
		return nil, err
	}
	if explicitLocation != "" {
		if len(locations) > 0 && !locationAllowed(explicitLocation, locations) {
			return nil, exterrors.Validation(
				"model_deployment_location_not_allowed",
				fmt.Sprintf(
					"deployment location %q is outside the allowed locations",
					explicitLocation,
				),
				"choose a deployment location from the allowed locations",
			)
		}
		return []string{explicitLocation}, nil
	}
	if projectLocation == "" {
		return locations, nil
	}
	if len(locations) > 0 && !locationAllowed(projectLocation, locations) {
		return nil, exterrors.Validation(
			"model_deployment_location_not_allowed",
			fmt.Sprintf(
				"the project location %q is outside the model's allowed locations",
				projectLocation,
			),
			"choose a model location that includes the project location",
		)
	}
	return []string{projectLocation}, nil
}

func azureContextLocation(azureContext *azdext.AzureContext) string {
	if azureContext == nil || azureContext.Scope == nil {
		return ""
	}
	return azureContext.Scope.Location
}

func nonEmptyStringSlice(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}

func resolveDeploymentCandidates(
	ctx context.Context,
	client *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	modelName string,
	options *azdext.AiModelDeploymentOptions,
) ([]*azdext.AiModelDeployment, error) {
	locations := options.GetLocations()
	if len(locations) <= 1 {
		return resolveDeploymentCandidatesAtLocation(
			ctx, client, azureContext, modelName, options,
			len(locations) == 1,
		)
	}

	var candidates []*azdext.AiModelDeployment
	var lastNoMatch error
	for _, location := range locations {
		locationOptions := *options
		locationOptions.Locations = []string{location}
		locationCandidates, err := resolveDeploymentCandidatesAtLocation(
			ctx, client, azureContext, modelName, &locationOptions, true,
		)
		if err != nil {
			if isDeploymentNoMatchError(err) {
				lastNoMatch = err
				continue
			}
			return nil, err
		}
		candidates = append(candidates, locationCandidates...)
	}
	if len(candidates) == 0 && lastNoMatch != nil {
		return nil, lastNoMatch
	}
	return candidates, nil
}

func isDeploymentNoMatchError(err error) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		st, ok := status.FromError(current)
		if !ok {
			continue
		}
		for _, detail := range st.Details() {
			info, ok := detail.(*errdetails.ErrorInfo)
			if ok && info.Domain == azdext.AiErrorDomain &&
				(info.Reason == azdext.AiErrorReasonModelNotFound ||
					info.Reason == azdext.AiErrorReasonNoDeploymentMatch) {
				return true
			}
		}
	}
	return false
}

func resolveDeploymentCandidatesAtLocation(
	ctx context.Context,
	client *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	modelName string,
	options *azdext.AiModelDeploymentOptions,
	checkQuota bool,
) ([]*azdext.AiModelDeployment, error) {
	request := &azdext.ResolveModelDeploymentsRequest{
		AzureContext: azureContext,
		ModelName:    modelName,
		Options:      options,
	}
	if checkQuota {
		request.Quota = &azdext.QuotaCheckOptions{MinRemainingCapacity: 1}
	}
	response, err := client.Ai().ResolveModelDeployments(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("resolve model deployment %q: %w", modelName, err)
	}
	return response.GetDeployments(), nil
}

func promptForDeploymentCandidate(
	ctx context.Context,
	client *azdext.AzdClient,
	candidates []*azdext.AiModelDeployment,
) (*azdext.AiModelDeployment, error) {
	choices := make([]*azdext.SelectChoice, len(candidates))
	for index, candidate := range candidates {
		choices[index] = &azdext.SelectChoice{
			Value: fmt.Sprintf("%d", index),
			Label: deploymentCandidateLabel(candidate),
		}
	}
	response, err := client.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a model deployment",
			Choices: choices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("model deployment selection was cancelled")
		}
		return nil, fmt.Errorf("select model deployment: %w", err)
	}
	index := int(response.GetValue())
	if index < 0 || index >= len(candidates) {
		return nil, exterrors.Validation(
			"model_deployment_selection_invalid",
			"the model deployment selection response was invalid",
			"retry model deployment selection",
		)
	}
	return candidates[index], nil
}

func deploymentCandidateLabel(candidate *azdext.AiModelDeployment) string {
	if candidate == nil {
		return "Unavailable deployment"
	}
	sku := ""
	if candidate.GetSku() != nil {
		sku = candidate.GetSku().GetName()
	}
	return fmt.Sprintf(
		"%s %s (%s, capacity %d, %s)",
		candidate.GetModelName(),
		candidate.GetVersion(),
		sku,
		candidate.GetCapacity(),
		candidate.GetLocation(),
	)
}

func deploymentCandidateKey(candidate *azdext.AiModelDeployment) string {
	if candidate == nil {
		return "~"
	}
	sku := ""
	if candidate.GetSku() != nil {
		sku = candidate.GetSku().GetName()
	}
	return strings.Join([]string{
		candidate.GetLocation(),
		candidate.GetModelName(),
		candidate.GetVersion(),
		sku,
		fmt.Sprintf("%09d", candidate.GetCapacity()),
	}, "\x00")
}

func reconcileDeployment(
	ctx context.Context,
	reconciler *projectServiceReconciler,
	serviceName string,
	requested synthesis.Deployment,
	force bool,
) (deploymentMutation, error) {
	service, _, err := reconciler.discoverProjectService(ctx)
	if err != nil {
		return "", err
	}
	if service == nil || service.Name != serviceName {
		return "", exterrors.Dependency(
			"project_service_not_found",
			fmt.Sprintf("project service %q was not found", serviceName),
			"run `azd ai project init` before adding a deployment",
		)
	}
	rawItems, resolvedItems, err := deploymentItems(service, reconciler.projectRoot)
	if err != nil {
		return "", err
	}
	seenNames := map[string]struct{}{}
	for _, item := range resolvedItems {
		name, ok := item["name"].(string)
		if !ok || strings.TrimSpace(name) == "" {
			return "", exterrors.Validation(
				"project_deployment_invalid",
				fmt.Sprintf("project service %q contains a deployment without a name", service.Name),
				"add a unique name to every deployment declaration",
			)
		}
		key := strings.ToLower(name)
		if _, exists := seenNames[key]; exists {
			return "", exterrors.Validation(
				"project_deployment_duplicate",
				fmt.Sprintf("project service %q contains duplicate deployment name %q", service.Name, name),
				"remove the duplicate deployment declarations and retry",
			)
		}
		seenNames[key] = struct{}{}
	}
	requestedName := strings.ToLower(requested.Name)
	for index, item := range resolvedItems {
		name := item["name"].(string)
		if strings.ToLower(name) != requestedName {
			continue
		}
		if !deploymentSemanticallyEqual(item, requested) {
			if service.ServiceRef != "" {
				return "", projectServiceRefError(service.Name, service.ServiceRef)
			}
			if index < len(rawItems) {
				if _, referenced := rawItems[index]["$ref"]; referenced {
					return "", exterrors.Validation(
						"project_deployment_ref_conflict",
						fmt.Sprintf("deployment %q is defined by a referenced file", name),
						"edit the referenced deployment file instead of using --force",
					)
				}
			}
			if !force {
				return "", exterrors.Validation(
					"project_deployment_conflict",
					fmt.Sprintf("deployment %q already exists with different settings", name),
					"use --force to replace the inline declaration",
				)
			}
			rawItems[index] = deploymentMap(requested)
			return deploymentMutationUpdate(ctx, reconciler, service.Name, rawItems, deploymentReplaced)
		}
		return deploymentUnchanged, nil
	}
	if service.ServiceRef != "" {
		return "", projectServiceRefError(service.Name, service.ServiceRef)
	}
	rawItems = append(rawItems, deploymentMap(requested))
	return deploymentMutationUpdate(ctx, reconciler, service.Name, rawItems, deploymentCreated)
}

func deploymentMutationUpdate(
	ctx context.Context,
	reconciler *projectServiceReconciler,
	serviceName string,
	items []map[string]any,
	mutation deploymentMutation,
) (deploymentMutation, error) {
	values := make([]any, len(items))
	for i := range items {
		values[i] = items[i]
	}
	value, err := structpb.NewValue(values)
	if err != nil {
		return "", fmt.Errorf("encode project deployments: %w", err)
	}
	if _, err := reconciler.client.Project().SetServiceConfigValue(ctx,
		&azdext.SetServiceConfigValueRequest{
			ServiceName: serviceName,
			Path:        "deployments",
			Value:       value,
		}); err != nil {
		return "", fmt.Errorf("update project service %q deployments: %w", serviceName, err)
	}
	return mutation, nil
}

func deploymentItems(
	service *projectServiceInfo,
	projectRoot string,
) ([]map[string]any, []map[string]any, error) {
	rawValue, _ := service.Raw["deployments"].([]any)
	resolvedSource, _ := service.Resolved["deployments"].([]any)
	var err error
	if rawValue == nil && resolvedSource != nil {
		rawValue = resolvedSource
	}
	if rawValue == nil {
		rawValue = []any{}
	}
	raw := make([]map[string]any, len(rawValue))
	for i, value := range rawValue {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("deployment item %d is not an object", i)
		}
		raw[i], err = cloneMap(item)
		if err != nil {
			return nil, nil, fmt.Errorf("copy deployment item %d: %w", i, err)
		}
	}
	resolvedMap := map[string]any{"deployments": rawValue}
	if projectRoot != "" {
		cloned, cloneErr := cloneMap(resolvedMap)
		if cloneErr != nil {
			return nil, nil, fmt.Errorf("copy project deployments: %w", cloneErr)
		}
		resolvedMap, err = foundry.ResolveFileRefs(cloned, projectRoot)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve project deployment references: %w", err)
		}
	}
	resolvedValue, _ := resolvedMap["deployments"].([]any)
	resolved := make([]map[string]any, len(resolvedValue))
	for i, value := range resolvedValue {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("resolved deployment item %d is not an object", i)
		}
		resolved[i] = item
	}
	if len(resolved) != len(raw) {
		return nil, nil, fmt.Errorf("resolved project deployments changed item count")
	}
	return raw, resolved, nil
}

func deploymentMap(deployment synthesis.Deployment) map[string]any {
	return map[string]any{
		"name": deployment.Name,
		"model": map[string]any{
			"format":  deployment.Model.Format,
			"name":    deployment.Model.Name,
			"version": deployment.Model.Version,
		},
		"sku": map[string]any{
			"name":     deployment.Sku.Name,
			"capacity": deployment.Sku.Capacity,
		},
	}
}

func deploymentSemanticallyEqual(value map[string]any, expected synthesis.Deployment) bool {
	model, _ := value["model"].(map[string]any)
	sku, _ := value["sku"].(map[string]any)
	name, _ := value["name"].(string)
	return strings.EqualFold(name, expected.Name) &&
		stringValue(model, "format") == expected.Model.Format &&
		stringValue(model, "name") == expected.Model.Name &&
		stringValue(model, "version") == expected.Model.Version &&
		stringValue(sku, "name") == expected.Sku.Name &&
		intValue(sku, "capacity") == expected.Sku.Capacity
}

func stringValue(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}

func intValue(value map[string]any, key string) int {
	switch result := value[key].(type) {
	case int:
		return result
	case int32:
		return int(result)
	case int64:
		return int(result)
	case float64:
		return int(result)
	default:
		return 0
	}
}
