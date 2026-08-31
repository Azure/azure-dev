// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

const agentsV2ModelCapability = "agentsV2"

var exactEnvironmentReference = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

var canonicalDeploymentEnvironmentKeyBases = [...]string{
	"AZURE_AI_MODEL_DEPLOYMENT_NAME",
	"AZURE_AI_MODEL_NAME",
	"AZURE_AI_MODEL_FORMAT",
	"AZURE_AI_MODEL_VERSION",
	"AZURE_AI_MODEL_SKU_NAME",
	"AZURE_AI_MODEL_SKU_CAPACITY",
}

type deploymentReferences struct {
	deploymentName string
	modelName      string
	modelFormat    string
	modelVersion   string
	skuName        string
	capacity       string
}

func canonicalDeploymentReferences(
	deployment synthesis.Deployment,
) (deploymentReferences, bool, error) {
	values := []string{
		deployment.Name,
		deployment.Model.Name,
		deployment.Model.Format,
		deployment.Model.Version,
		deployment.Sku.Name,
	}
	if capacity, ok := deployment.Sku.Capacity.(string); ok {
		values = append(values, capacity)
	} else {
		values = append(values, "")
	}

	index := -1
	canonicalCount := 0
	hasOtherReference := false
	for _, value := range values {
		match := exactEnvironmentReference.FindStringSubmatch(
			strings.TrimSpace(value),
		)
		if len(match) == 2 {
			candidate, ok := canonicalDeploymentEnvironmentKeyIndex(match[1])
			if !ok {
				hasOtherReference = true
				continue
			}
			canonicalCount++
			if index >= 0 && index != candidate {
				return deploymentReferences{}, false, fmt.Errorf(
					"canonical environment references use mismatched suffixes",
				)
			}
			index = candidate
			continue
		}
		if strings.Contains(strings.TrimSpace(value), "${") {
			hasOtherReference = true
		}
	}
	if canonicalCount == 0 {
		return deploymentReferences{}, false, nil
	}
	if hasOtherReference || canonicalCount != len(values) {
		return deploymentReferences{}, false, fmt.Errorf(
			"deployment must use one complete canonical environment tuple",
		)
	}

	expected := deploymentReferences{
		deploymentName: indexedDeploymentKey(
			"AZURE_AI_MODEL_DEPLOYMENT_NAME", index),
		modelName:    indexedDeploymentKey("AZURE_AI_MODEL_NAME", index),
		modelFormat:  indexedDeploymentKey("AZURE_AI_MODEL_FORMAT", index),
		modelVersion: indexedDeploymentKey("AZURE_AI_MODEL_VERSION", index),
		skuName:      indexedDeploymentKey("AZURE_AI_MODEL_SKU_NAME", index),
		capacity:     indexedDeploymentKey("AZURE_AI_MODEL_SKU_CAPACITY", index),
	}
	return expected, true, nil
}

func indexedDeploymentKey(base string, index int) string {
	if index == 0 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, index+1)
}

func environmentReference(value string) string {
	match := exactEnvironmentReference.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 2 {
		return ""
	}
	return match[1]
}

func canonicalDeploymentEnvironmentKeyIndex(key string) (int, bool) {
	for _, base := range canonicalDeploymentEnvironmentKeyBases {
		if key == base {
			return 0, true
		}
		suffix, ok := strings.CutPrefix(key, base+"_")
		if !ok || suffix == "" || suffix[0] == '0' {
			continue
		}
		number, err := strconv.Atoi(suffix)
		if err == nil && number >= 2 {
			return number - 1, true
		}
	}
	return 0, false
}

func isCanonicalDeploymentEnvironmentKey(key string) bool {
	_, ok := canonicalDeploymentEnvironmentKeyIndex(key)
	return ok
}

func (r deploymentReferences) keys() []string {
	return []string{
		r.deploymentName,
		r.modelName,
		r.modelFormat,
		r.modelVersion,
		r.skuName,
		r.capacity,
	}
}

func (p *FoundryProvisioningProvider) reconcileDeploymentEnvironment(
	ctx context.Context,
	rawYAML []byte,
	serviceName string,
) error {
	p.resolvedDeploymentEnv = nil
	deployments, err := synthesis.ProjectDeployments(rawYAML, serviceName, p.projectPath)
	if err != nil {
		return foundrySynthesisError(serviceName, err)
	}
	hasCanonicalDeployment := false
	for i, deployment := range deployments {
		_, canonical, err := canonicalDeploymentReferences(deployment)
		if err != nil {
			return fmt.Errorf(
				"validate model deployment %d references: %w",
				i+1,
				err,
			)
		}
		if canonical {
			hasCanonicalDeployment = true
			break
		}
	}
	if !hasCanonicalDeployment {
		return nil
	}

	env, err := p.deploymentEnvironmentMap(ctx)
	if err != nil {
		return err
	}
	if p.virtualEnv == nil {
		p.virtualEnv = map[string]string{}
	}
	p.resolvedDeploymentEnv = map[string]string{}
	for key := range p.virtualEnv {
		if isCanonicalDeploymentEnvironmentKey(key) {
			delete(p.virtualEnv, key)
		}
	}
	for key, value := range env {
		if strings.TrimSpace(value) != "" {
			trimmed := strings.TrimSpace(value)
			p.virtualEnv[key] = trimmed
			if isCanonicalDeploymentEnvironmentKey(key) {
				p.resolvedDeploymentEnv[key] = trimmed
			}
		}
	}
	for i, deployment := range deployments {
		references, canonical, err := canonicalDeploymentReferences(deployment)
		if err != nil {
			return fmt.Errorf(
				"validate model deployment %d references: %w",
				i+1,
				err,
			)
		}
		if !canonical {
			continue
		}
		keys := references.keys()

		missing := make([]string, 0, len(keys))
		for _, key := range keys {
			if strings.TrimSpace(env[key]) == "" {
				missing = append(missing, key)
			}
		}
		issue := deploymentEnvironmentMissing
		if len(missing) == 0 {
			valid, err := p.validateDeploymentEnvironment(ctx, references, env)
			if err != nil {
				return fmt.Errorf(
					"validate model deployment %d environment: %w",
					i+1,
					err,
				)
			}
			if valid {
				continue
			}
			missing = keys
			issue = deploymentEnvironmentIncompatible
		}

		resolved, err := p.promptDeploymentEnvironment(
			ctx, deployment, references, i+1, issue, missing, env)
		if err != nil {
			return err
		}
		values := map[string]string{
			references.deploymentName: resolved.deploymentName,
			references.modelName:      resolved.modelName,
			references.modelFormat:    resolved.modelFormat,
			references.modelVersion:   resolved.modelVersion,
			references.skuName:        resolved.skuName,
			references.capacity:       resolved.capacity,
		}
		for _, key := range keys {
			if err := p.setEnv(ctx, key, values[key]); err != nil {
				return exterrors.Dependency(
					exterrors.CodeEnvironmentValuesFailed,
					fmt.Sprintf("persist deployment %d environment value %s: %s", i+1, key, err),
					"verify the azd environment is writable, then retry",
				)
			}
			env[key] = values[key]
			if p.virtualEnv == nil {
				p.virtualEnv = map[string]string{}
			}
			p.virtualEnv[key] = values[key]
			p.resolvedDeploymentEnv[key] = values[key]
		}
	}
	return nil
}

// deploymentEnvironmentMap reads the active azd environment without falling
// back to process variables. Canonical deployment tuples must not be satisfied
// by values from another environment or shell.
func (p *FoundryProvisioningProvider) deploymentEnvironmentMap(
	ctx context.Context,
) (map[string]string, error) {
	out := make(map[string]string, len(p.virtualEnv))
	for key, value := range p.virtualEnv {
		if !isCanonicalDeploymentEnvironmentKey(key) {
			out[key] = value
		}
	}
	if p.azdClient == nil || p.azdClient.Environment() == nil {
		return nil, exterrors.Dependency(
			exterrors.CodeAzdClientFailed,
			"read model deployment environment: azd environment client is unavailable",
			"restart azd and retry",
		)
	}

	response, err := p.azdClient.Environment().GetValues(
		ctx,
		&azdext.GetEnvironmentRequest{Name: p.envName},
	)
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf(
				"read model deployment values from azd environment %q: %s",
				p.envName,
				err,
			),
			"verify the azd environment is accessible, then retry",
		)
	}
	for _, keyValue := range response.GetKeyValues() {
		if keyValue == nil {
			continue
		}
		value := strings.TrimSpace(keyValue.Value)
		if isCanonicalDeploymentEnvironmentKey(keyValue.Key) {
			out[keyValue.Key] = value
		} else if _, planned := out[keyValue.Key]; !planned {
			out[keyValue.Key] = value
		}
	}
	return out, nil
}

type resolvedDeploymentEnvironment struct {
	deploymentName string
	modelName      string
	modelFormat    string
	modelVersion   string
	skuName        string
	capacity       string
}

type deploymentEnvironmentIssue string

const (
	deploymentEnvironmentMissing      deploymentEnvironmentIssue = "missing"
	deploymentEnvironmentIncompatible deploymentEnvironmentIssue = "incompatible"
)

func (p *FoundryProvisioningProvider) promptDeploymentEnvironment(
	ctx context.Context,
	deployment synthesis.Deployment,
	references deploymentReferences,
	deploymentIndex int,
	issue deploymentEnvironmentIssue,
	affectedKeys []string,
	env map[string]string,
) (resolvedDeploymentEnvironment, error) {
	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			SubscriptionId: p.subID,
			Location:       p.location,
			TenantId:       p.tenantID,
		},
	}
	defaultModel := env[references.modelName]
	if defaultModel == "" && environmentReference(deployment.Model.Name) == "" {
		defaultModel = deployment.Model.Name
	}

	modelResponse, err := p.azdClient.Prompt().PromptAiModel(ctx, &azdext.PromptAiModelRequest{
		AzureContext: azureContext,
		Filter: &azdext.AiModelFilterOptions{
			Locations:    []string{p.location},
			Capabilities: []string{agentsV2ModelCapability},
		},
		Quota:        &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		DefaultValue: defaultModel,
		SelectOptions: &azdext.SelectOptions{
			Message: "Select a model for this azd environment",
		},
	})
	if err != nil {
		return resolvedDeploymentEnvironment{}, p.deploymentPromptError(
			err, deploymentIndex, issue, affectedKeys)
	}
	model := modelResponse.GetModel()
	if model == nil || strings.TrimSpace(model.GetName()) == "" {
		return resolvedDeploymentEnvironment{}, exterrors.Internal(
			exterrors.CodeMissingModelDeployment,
			"model selection returned an empty model",
		)
	}

	deploymentResponse, err := p.promptAiDeployment(
		ctx, azureContext, model.GetName(), new(int32(50)))
	if err != nil && hasAiErrorReason(
		err,
		azdext.AiErrorReasonNoValidSkus,
		azdext.AiErrorReasonNoDeploymentMatch,
	) {
		deploymentResponse, err = p.promptAiDeployment(
			ctx, azureContext, model.GetName(), nil)
	}
	if err != nil {
		return resolvedDeploymentEnvironment{}, p.deploymentPromptError(
			err, deploymentIndex, issue, affectedKeys)
	}
	selected := deploymentResponse.GetDeployment()
	if selected == nil || selected.GetSku() == nil {
		return resolvedDeploymentEnvironment{}, exterrors.Internal(
			exterrors.CodeMissingModelDeployment,
			"model deployment selection returned an empty deployment",
		)
	}

	deploymentName := model.GetName()
	if currentName := strings.TrimSpace(env[references.deploymentName]); currentName != "" {
		deploymentName = currentName
	} else if environmentReference(deployment.Name) == "" &&
		strings.TrimSpace(deployment.Name) != "" {
		deploymentName = deployment.Name
	}
	return resolvedDeploymentEnvironment{
		deploymentName: deploymentName,
		modelName:      selected.GetModelName(),
		modelFormat:    selected.GetFormat(),
		modelVersion:   selected.GetVersion(),
		skuName:        selected.GetSku().GetName(),
		capacity:       strconv.Itoa(int(selected.GetCapacity())),
	}, nil
}

func (p *FoundryProvisioningProvider) promptAiDeployment(
	ctx context.Context,
	azureContext *azdext.AzureContext,
	modelName string,
	capacity *int32,
) (*azdext.PromptAiDeploymentResponse, error) {
	return p.azdClient.Prompt().PromptAiDeployment(
		ctx,
		&azdext.PromptAiDeploymentRequest{
			AzureContext: azureContext,
			ModelName:    modelName,
			Options: &azdext.AiModelDeploymentOptions{
				Locations: []string{p.location},
				Capacity:  capacity,
			},
			Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		},
	)
}

func (p *FoundryProvisioningProvider) validateDeploymentEnvironment(
	ctx context.Context,
	references deploymentReferences,
	env map[string]string,
) (bool, error) {
	modelName := strings.TrimSpace(env[references.modelName])
	modelFormat := strings.TrimSpace(env[references.modelFormat])
	modelVersion := strings.TrimSpace(env[references.modelVersion])
	skuName := strings.TrimSpace(env[references.skuName])
	capacityValue, err := strconv.ParseInt(strings.TrimSpace(
		env[references.capacity]), 10, 32)
	if err != nil || modelName == "" || modelFormat == "" ||
		modelVersion == "" || skuName == "" || capacityValue <= 0 {
		return false, nil
	}
	capacity := int32(capacityValue)

	modelResponse, err := p.azdClient.Ai().ListModels(ctx,
		&azdext.ListModelsRequest{
			AzureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: p.subID,
					Location:       p.location,
				},
			},
			Filter: &azdext.AiModelFilterOptions{
				Locations:    []string{p.location},
				Capabilities: []string{agentsV2ModelCapability},
			},
		},
	)
	if err != nil {
		if hasAiErrorReason(
			err,
			azdext.AiErrorReasonNoModelsMatch,
			azdext.AiErrorReasonModelNotFound,
		) {
			return false, nil
		}
		return false, err
	}
	modelAvailable := slices.ContainsFunc(modelResponse.GetModels(),
		func(model *azdext.AiModel) bool {
			return model != nil &&
				strings.EqualFold(model.GetName(), modelName) &&
				slices.Contains(model.GetCapabilities(), agentsV2ModelCapability)
		})
	if !modelAvailable {
		return false, nil
	}

	response, err := p.azdClient.Ai().ResolveModelDeployments(ctx,
		&azdext.ResolveModelDeploymentsRequest{
			AzureContext: &azdext.AzureContext{
				Scope: &azdext.AzureScope{
					SubscriptionId: p.subID,
					Location:       p.location,
				},
			},
			ModelName: modelName,
			Options: &azdext.AiModelDeploymentOptions{
				Locations: []string{p.location},
				Versions:  []string{modelVersion},
				Skus:      []string{skuName},
				Capacity:  new(capacity),
			},
			Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		})
	if err != nil {
		if hasAiErrorReason(
			err,
			azdext.AiErrorReasonModelNotFound,
			azdext.AiErrorReasonNoDeploymentMatch,
		) {
			return false, nil
		}
		return false, err
	}

	for _, candidate := range response.GetDeployments() {
		if candidate.GetFormat() == modelFormat &&
			candidate.GetVersion() == modelVersion &&
			candidate.GetSku().GetName() == skuName &&
			candidate.GetCapacity() == capacity {
			return true, nil
		}
	}
	return false, nil
}

func hasAiErrorReason(err error, reasons ...string) bool {
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

func (p *FoundryProvisioningProvider) deploymentPromptError(
	err error,
	deploymentIndex int,
	issue deploymentEnvironmentIssue,
	affectedKeys []string,
) error {
	if exterrors.IsCancellation(err) {
		return exterrors.Cancelled("model deployment selection was cancelled")
	}
	if exterrors.IsPromptRequired(err) {
		return exterrors.Dependency(
			exterrors.CodeMissingModelDeployment,
			fmt.Sprintf(
				"model deployment %d environment values are %s in azd environment %q: %s",
				deploymentIndex,
				issue,
				p.envName,
				strings.Join(affectedKeys, ", "),
			),
			"set the complete deployment tuple with `azd env set <name> <value>`, "+
				"or run interactively to select a compatible model",
		)
	}
	return exterrors.Dependency(
		exterrors.CodeMissingModelDeployment,
		fmt.Sprintf(
			"select a compatible model for deployment %d with %s "+
				"environment values (%s): %s",
			deploymentIndex,
			issue,
			strings.Join(affectedKeys, ", "),
			err,
		),
		"retry, or set the model deployment values in the azd environment",
	)
}
