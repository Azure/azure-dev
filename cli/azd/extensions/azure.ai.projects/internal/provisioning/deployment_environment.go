// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const agentsV2ModelCapability = "agentsV2"

var exactEnvironmentReference = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

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
	index int,
) (deploymentReferences, bool) {
	references := referencesForDeployment(deployment)
	expected := deploymentReferences{
		deploymentName: indexedDeploymentKey(
			"AZURE_AI_MODEL_DEPLOYMENT_NAME", index),
		modelName:    indexedDeploymentKey("AZURE_AI_MODEL_NAME", index),
		modelFormat:  indexedDeploymentKey("AZURE_AI_MODEL_FORMAT", index),
		modelVersion: indexedDeploymentKey("AZURE_AI_MODEL_VERSION", index),
		skuName:      indexedDeploymentKey("AZURE_AI_MODEL_SKU_NAME", index),
		capacity:     indexedDeploymentKey("AZURE_AI_MODEL_SKU_CAPACITY", index),
	}
	if references != expected {
		return deploymentReferences{}, false
	}
	return expected, true
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

func referencesForDeployment(deployment synthesis.Deployment) deploymentReferences {
	capacity, _ := deployment.Sku.Capacity.(string)
	return deploymentReferences{
		deploymentName: environmentReference(deployment.Name),
		modelName:      environmentReference(deployment.Model.Name),
		modelFormat:    environmentReference(deployment.Model.Format),
		modelVersion:   environmentReference(deployment.Model.Version),
		skuName:        environmentReference(deployment.Sku.Name),
		capacity:       environmentReference(capacity),
	}
}

func (r deploymentReferences) keys() []string {
	keys := []string{
		r.deploymentName,
		r.modelName,
		r.modelFormat,
		r.modelVersion,
		r.skuName,
		r.capacity,
	}
	nonEmpty := keys[:0]
	for _, key := range keys {
		if key != "" {
			nonEmpty = append(nonEmpty, key)
		}
	}
	return nonEmpty
}

func (p *FoundryProvisioningProvider) reconcileDeploymentEnvironment(
	ctx context.Context,
	rawYAML []byte,
	serviceName string,
) error {
	deployments, err := synthesis.ProjectDeployments(rawYAML, serviceName, p.projectPath)
	if err != nil {
		return foundrySynthesisError(serviceName, err)
	}
	env := p.networkEnvMap(ctx)
	for i, deployment := range deployments {
		references, canonical := canonicalDeploymentReferences(deployment, i)
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
		if len(missing) == 0 {
			valid, err := p.validateDeploymentEnvironment(ctx, references, env)
			if err != nil {
				return p.deploymentPromptError(err, keys)
			}
			if valid {
				continue
			}
			missing = keys
		}

		resolved, err := p.promptDeploymentEnvironment(
			ctx, deployment, references, missing, env)
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
		}
	}
	return nil
}

type resolvedDeploymentEnvironment struct {
	deploymentName string
	modelName      string
	modelFormat    string
	modelVersion   string
	skuName        string
	capacity       string
}

func (p *FoundryProvisioningProvider) promptDeploymentEnvironment(
	ctx context.Context,
	deployment synthesis.Deployment,
	references deploymentReferences,
	missing []string,
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
		return resolvedDeploymentEnvironment{}, p.deploymentPromptError(err, missing)
	}
	model := modelResponse.GetModel()
	if model == nil || strings.TrimSpace(model.GetName()) == "" {
		return resolvedDeploymentEnvironment{}, exterrors.Internal(
			exterrors.CodeMissingModelDeployment,
			"model selection returned an empty model",
		)
	}

	deploymentResponse, err := p.azdClient.Prompt().PromptAiDeployment(
		ctx,
		&azdext.PromptAiDeploymentRequest{
			AzureContext: azureContext,
			ModelName:    model.GetName(),
			Options: &azdext.AiModelDeploymentOptions{
				Locations: []string{p.location},
				Capacity:  new(int32(50)),
			},
			Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		},
	)
	if err != nil {
		return resolvedDeploymentEnvironment{}, p.deploymentPromptError(err, missing)
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

func (p *FoundryProvisioningProvider) validateDeploymentEnvironment(
	ctx context.Context,
	references deploymentReferences,
	env map[string]string,
) (bool, error) {
	modelName := strings.TrimSpace(env[references.modelName])
	modelFormat := strings.TrimSpace(env[references.modelFormat])
	modelVersion := strings.TrimSpace(env[references.modelVersion])
	skuName := strings.TrimSpace(env[references.skuName])
	capacity, err := strconv.Atoi(strings.TrimSpace(
		env[references.capacity]))
	if err != nil || modelName == "" || modelFormat == "" ||
		modelVersion == "" || skuName == "" {
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
				Capacity:  new(int32(capacity)),
			},
			Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		})
	if err != nil {
		return false, nil
	}

	for _, candidate := range response.GetDeployments() {
		if candidate.GetFormat() == modelFormat &&
			candidate.GetVersion() == modelVersion &&
			candidate.GetSku().GetName() == skuName &&
			int(candidate.GetCapacity()) == capacity {
			return true, nil
		}
	}
	return false, nil
}

func (p *FoundryProvisioningProvider) deploymentPromptError(err error, missing []string) error {
	if exterrors.IsCancellation(err) {
		return exterrors.Cancelled("model deployment selection was cancelled")
	}
	if exterrors.IsPromptRequired(err) {
		return exterrors.Dependency(
			exterrors.CodeMissingModelDeployment,
			fmt.Sprintf(
				"model deployment environment values are missing or incompatible in azd environment %q: %s",
				p.envName,
				strings.Join(missing, ", "),
			),
			"set the listed values with `azd env set <name> <value>`, or run interactively to select a compatible model",
		)
	}
	return exterrors.Dependency(
		exterrors.CodeMissingModelDeployment,
		fmt.Sprintf("select a compatible model deployment: %s", err),
		"retry, or set the model deployment values in the azd environment",
	)
}
