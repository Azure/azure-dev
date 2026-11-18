// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"azure.ai.projects/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

// ProjectDeploymentsEnvKey holds the active environment's managed model deployments.
const ProjectDeploymentsEnvKey = "AI_PROJECT_DEPLOYMENTS"

// DefaultDeploymentCapacity is the preferred deployment capacity for project model deployments
// when a declaration does not specify an explicit positive capacity.
const DefaultDeploymentCapacity int32 = 250

// EncodeProjectDeployments encodes deployments using the azd environment escape contract.
func EncodeProjectDeployments(deployments []synthesis.Deployment) (string, error) {
	if deployments == nil {
		deployments = []synthesis.Deployment{}
	}
	data, err := json.Marshal(deployments)
	if err != nil {
		return "", fmt.Errorf("encoding project deployments: %w", err)
	}
	escaped := strings.ReplaceAll(string(data), "\\", "\\\\")
	return strings.ReplaceAll(escaped, "\"", "\\\""), nil
}

// DecodeProjectDeployments accepts JSON or environment-escaped JSON. Blank values
// are missing; an empty array is an intentional override.
func DecodeProjectDeployments(value string) ([]synthesis.Deployment, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false, nil
	}
	if !json.Valid([]byte(value)) {
		var unescaped string
		if err := json.Unmarshal([]byte(`"`+value+`"`), &unescaped); err != nil {
			return nil, true, invalidProjectDeployments(err)
		}
		value = unescaped
	}
	if !strings.HasPrefix(value, "[") {
		return nil, true, invalidProjectDeployments(fmt.Errorf("expected a JSON array"))
	}
	var deployments []*synthesis.Deployment
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&deployments); err != nil {
		return nil, true, invalidProjectDeployments(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, true, invalidProjectDeployments(fmt.Errorf("expected a single JSON array"))
	}
	result := make([]synthesis.Deployment, 0, len(deployments))
	for i, deployment := range deployments {
		if deployment == nil {
			return nil, true, invalidProjectDeployments(fmt.Errorf("deployment entries must be objects, not null"))
		}
		for _, field := range []struct{ name, value string }{
			{"name", deployment.Name},
			{"model.format", deployment.Model.Format},
			{"model.name", deployment.Model.Name},
			{"model.version", deployment.Model.Version},
			{"sku.name", deployment.Sku.Name},
		} {
			if strings.TrimSpace(field.value) == "" {
				return nil, true, invalidProjectDeployments(
					fmt.Errorf("deployment entry %d requires %s", i, field.name),
				)
			}
		}
		if deployment.Sku.Capacity <= 0 {
			return nil, true, invalidProjectDeployments(
				fmt.Errorf("deployment entry %d requires sku.capacity to be a positive integer", i),
			)
		}
		result = append(result, *deployment)
	}
	return result, true, nil
}

func invalidProjectDeployments(err error) error {
	return fmt.Errorf(
		"invalid %s: %w; set it to a JSON array of deployments (or [] for no deployments) using azd env set",
		ProjectDeploymentsEnvKey, err,
	)
}

// ReadProjectDeployments reads only the named azd environment, not process defaults.
func ReadProjectDeployments(
	ctx context.Context, client *azdext.AzdClient, envName string,
) ([]synthesis.Deployment, bool, error) {
	response, err := client.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: envName})
	if err != nil {
		return nil, false, fmt.Errorf("read %s from azd environment %q: %w", ProjectDeploymentsEnvKey, envName, err)
	}
	for _, kv := range response.GetKeyValues() {
		if kv.GetKey() == ProjectDeploymentsEnvKey {
			return DecodeProjectDeployments(kv.GetValue())
		}
	}
	return nil, false, nil
}

// PromptProjectDeployments resolves portable declarations for the current Azure scope.
func PromptProjectDeployments(
	ctx context.Context,
	client *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	declarations []synthesis.Deployment,
) ([]synthesis.Deployment, error) {
	deployments := make([]synthesis.Deployment, 0, len(declarations))
	for _, declaration := range declarations {
		model, err := client.Prompt().PromptAiModel(ctx, &azdext.PromptAiModelRequest{
			AzureContext: azureContext,
			DefaultValue: declaration.Model.Name,
			Filter: &azdext.AiModelFilterOptions{
				Locations: []string{azureContext.GetScope().GetLocation()},
			},
			Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		})
		if err != nil {
			return nil, fmt.Errorf("select model for deployment %q: %w", declaration.Name, err)
		}
		if model.GetModel().GetName() == "" {
			return nil, fmt.Errorf("no model selected for deployment %q", declaration.Name)
		}
		preferredCapacity := DefaultDeploymentCapacity
		if declaration.Sku.Capacity > 0 && declaration.Sku.Capacity <= math.MaxInt32 {
			preferredCapacity = int32(declaration.Sku.Capacity)
		}
		response, err := promptAiDeploymentWithFallback(
			ctx,
			client,
			azureContext,
			model.GetModel(),
			preferredCapacity,
		)
		if err != nil {
			return nil, fmt.Errorf("select deployment %q: %w", declaration.Name, err)
		}
		selected := response.GetDeployment()
		if selected.GetModelName() == "" || selected.GetVersion() == "" ||
			selected.GetSku().GetName() == "" || selected.GetCapacity() <= 0 {
			return nil, fmt.Errorf("incomplete model selection for deployment %q", declaration.Name)
		}
		deployments = append(deployments, synthesis.Deployment{
			Name: declaration.Name,
			Model: synthesis.DeploymentModel{
				Name: selected.GetModelName(), Format: selected.GetFormat(), Version: selected.GetVersion(),
			},
			Sku: synthesis.DeploymentSku{Name: selected.GetSku().GetName(), Capacity: int(selected.GetCapacity())},
		})
	}
	return deployments, nil
}

var defaultSkuPriority = []string{"GlobalStandard", "DataZoneStandard", "Standard"}

// SkuPriority returns the relative priority of a deployment SKU (lower is higher priority).
func SkuPriority(skuName string) int {
	for i, preferred := range defaultSkuPriority {
		if preferred == skuName {
			return i
		}
	}
	return len(defaultSkuPriority)
}

// DeploymentCapacityBaseline returns the minimum valid capacity baseline for a SKU.
func DeploymentCapacityBaseline(sku *azdext.AiModelSku) int32 {
	if minCapacity := sku.GetMinCapacity(); minCapacity > 0 {
		return minCapacity
	}
	if capacityStep := sku.GetCapacityStep(); capacityStep > 0 {
		return capacityStep
	}
	return 1
}

// DeploymentCapacityValid reports whether capacity satisfies the SKU's min, max, and step constraints.
func DeploymentCapacityValid(sku *azdext.AiModelSku, capacity int32) bool {
	if capacity <= 0 {
		return false
	}
	if minCapacity := sku.GetMinCapacity(); minCapacity > 0 && capacity < minCapacity {
		return false
	}
	if maxCapacity := sku.GetMaxCapacity(); maxCapacity > 0 && capacity > maxCapacity {
		return false
	}
	if capacityStep := sku.GetCapacityStep(); capacityStep > 0 {
		baseline := DeploymentCapacityBaseline(sku)
		if capacity < baseline || (capacity-baseline)%capacityStep != 0 {
			return false
		}
	}
	return true
}

// ClampDeploymentCapacity clamps preferredCapacity downward to the SKU's max, step, and remaining quota bounds.
func ClampDeploymentCapacity(
	candidate *azdext.AiModelDeployment,
	preferredCapacity int32,
) (int32, bool) {
	sku := candidate.GetSku()
	if sku == nil || preferredCapacity <= 0 {
		return 0, false
	}
	lowerBound := DeploymentCapacityBaseline(sku)
	upperBound := max(lowerBound, preferredCapacity)
	if maxCapacity := sku.GetMaxCapacity(); maxCapacity > 0 && upperBound > maxCapacity {
		upperBound = maxCapacity
	}
	if candidate.RemainingQuota != nil {
		remaining := int32(candidate.GetRemainingQuota())
		if remaining < upperBound {
			upperBound = remaining
		}
	}
	if upperBound < lowerBound {
		return 0, false
	}
	if step := sku.GetCapacityStep(); step > 0 {
		upperBound -= (upperBound - lowerBound) % step
	}
	if !DeploymentCapacityValid(sku, upperBound) {
		return 0, false
	}
	if candidate.RemainingQuota != nil && float64(upperBound) > candidate.GetRemainingQuota() {
		return 0, false
	}
	return upperBound, true
}

// IsDeploymentCapacityFallbackError reports whether err is a gRPC AI error caused by capacity/SKU filtering.
func IsDeploymentCapacityFallbackError(err error) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		st, ok := status.FromError(current)
		if !ok {
			continue
		}
		for _, detail := range st.Details() {
			info, ok := detail.(*errdetails.ErrorInfo)
			if ok && info.Domain == azdext.AiErrorDomain &&
				(info.Reason == azdext.AiErrorReasonNoValidSkus ||
					info.Reason == azdext.AiErrorReasonNoDeploymentMatch) {
				return true
			}
		}
	}
	return false
}

func promptAiDeploymentWithFallback(
	ctx context.Context,
	client *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	model *azdext.AiModel,
	preferredCapacity int32,
) (*azdext.PromptAiDeploymentResponse, error) {
	modelName := model.GetName()
	locations := []string{azureContext.GetScope().GetLocation()}
	if candidate, clamped, scopeCandidate, ok := resolveClampedPromptDeployment(
		ctx, client, azureContext, model, locations, preferredCapacity,
	); ok {
		retryOpts := &azdext.AiModelDeploymentOptions{
			Locations: locations,
			Capacity:  new(clamped),
		}
		if scopeCandidate {
			if version := candidate.GetVersion(); version != "" {
				retryOpts.Versions = []string{version}
			}
			if skuName := candidate.GetSku().GetName(); skuName != "" {
				retryOpts.Skus = []string{skuName}
			}
		}
		response, err := client.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
			AzureContext: azureContext,
			ModelName:    modelName,
			Options:      retryOpts,
			Quota:        &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		})
		if err == nil || !IsDeploymentCapacityFallbackError(err) {
			return response, err
		}
	} else {
		response, err := client.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
			AzureContext: azureContext,
			ModelName:    modelName,
			Options: &azdext.AiModelDeploymentOptions{
				Locations: locations,
				Capacity:  new(preferredCapacity),
			},
			Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		})
		if err == nil || !IsDeploymentCapacityFallbackError(err) {
			return response, err
		}
	}

	return client.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
		AzureContext: azureContext,
		ModelName:    modelName,
		Options: &azdext.AiModelDeploymentOptions{
			Locations: locations,
		},
		Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
	})
}

func resolveClampedPromptDeployment(
	ctx context.Context,
	client *azdext.AzdClient,
	azureContext *azdext.AzureContext,
	model *azdext.AiModel,
	locations []string,
	preferredCapacity int32,
) (*azdext.AiModelDeployment, int32, bool, bool) {
	resolveResp, err := client.Ai().ResolveModelDeployments(ctx, &azdext.ResolveModelDeploymentsRequest{
		AzureContext: azureContext,
		ModelName:    model.GetName(),
		Options: &azdext.AiModelDeploymentOptions{
			Locations: locations,
		},
		Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
	})
	if err != nil || len(resolveResp.GetDeployments()) == 0 {
		return nil, 0, false, false
	}

	defaultVersions := make(map[string]struct{}, len(model.GetVersions()))
	for _, v := range model.GetVersions() {
		if v.GetIsDefault() {
			defaultVersions[v.GetVersion()] = struct{}{}
		}
	}

	var bestCandidate *azdext.AiModelDeployment
	var bestCap int32
	bestDefault := false
	bestPriority := len(defaultSkuPriority) + 1
	for _, candidate := range resolveResp.GetDeployments() {
		clamped, ok := ClampDeploymentCapacity(candidate, preferredCapacity)
		if !ok || clamped <= 0 {
			continue
		}
		_, isDefault := defaultVersions[candidate.GetVersion()]
		priority := SkuPriority(candidate.GetSku().GetName())
		better := false
		if bestCandidate == nil {
			better = true
		} else if isDefault != bestDefault {
			better = isDefault
		} else if priority != bestPriority {
			better = priority < bestPriority
		} else if clamped != bestCap {
			better = clamped > bestCap
		}
		if better {
			bestCandidate = candidate
			bestCap = clamped
			bestDefault = isDefault
			bestPriority = priority
		}
	}
	if bestCandidate == nil || bestCap >= preferredCapacity {
		return nil, 0, false, false
	}
	scopeCandidate := needsPromptCandidateScoping(
		resolveResp.GetDeployments(), bestCap, preferredCapacity,
	)
	return bestCandidate, bestCap, scopeCandidate, true
}

func needsPromptCandidateScoping(
	candidates []*azdext.AiModelDeployment,
	targetCapacity int32,
	preferredCapacity int32,
) bool {
	for _, candidate := range candidates {
		if candidate == nil || candidate.GetSku() == nil {
			continue
		}
		if !DeploymentCapacityValid(candidate.GetSku(), targetCapacity) {
			return true
		}
		if clamped, ok := ClampDeploymentCapacity(candidate, preferredCapacity); ok && clamped > targetCapacity {
			return true
		}
	}
	return false
}
