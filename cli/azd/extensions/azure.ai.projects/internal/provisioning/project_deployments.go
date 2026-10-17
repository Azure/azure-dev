// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ProjectDeploymentsEnvKey holds the active environment's managed model deployments.
const ProjectDeploymentsEnvKey = "AI_PROJECT_DEPLOYMENTS"

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
		response, err := client.Prompt().PromptAiDeployment(ctx, &azdext.PromptAiDeploymentRequest{
			AzureContext: azureContext,
			ModelName:    model.GetModel().GetName(),
			Options: &azdext.AiModelDeploymentOptions{
				Locations: []string{azureContext.GetScope().GetLocation()},
			},
			Quota: &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		})
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
