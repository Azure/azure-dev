// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ResolvePromptAgentModel returns the deployment a prompt agent should use in
// the current environment.
//
// ${VAR} references expand first. Explicit selections are stored per
// declaration in AI_PROJECT_DEPLOYMENT_BINDINGS without rewriting YAML.
// Legacy environments infer a single replacement from the managed list
// and AZURE_AI_MODEL_DEPLOYMENT_NAME. Explicit bindings take precedence,
// including after infrastructure ejection.
// On-disk infrastructure never infers bindings from the managed list.
func ResolvePromptAgentModel(
	model string,
	agentService *azdext.ServiceConfig,
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
	env map[string]string,
) (string, error) {
	lookup := func(name string) string {
		if value, ok := env[name]; ok {
			return value
		}
		value, _ := os.LookupEnv(name)
		return value
	}
	expanded, err := ExpandEnv(strings.TrimSpace(model), lookup)
	if err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to expand model %q: %s", model, err),
			"check the ${VAR} reference in the agent definition's model",
		)
	}
	model = strings.TrimSpace(expanded)

	bindings, err := synthesis.DecodeDeploymentBindings(env[synthesis.DeploymentBindingsEnvKey])
	if err != nil {
		return "", err
	}
	replacement, bound, err := promptModelDeploymentBinding(bindings, model)
	if err != nil {
		return "", err
	}
	if bound {
		declared, err := usedProjectDeploymentNames(agentService, services, projectRoot, lookup)
		if err != nil {
			return "", err
		}
		if containsDeploymentName(declared, model) {
			if _, err := promptModelInfrastructureOnDisk(projectRoot); err != nil {
				return "", err
			}
			if replacement == "" {
				return "", exterrors.Dependency(
					exterrors.CodeModelDeploymentNotFound,
					fmt.Sprintf("model deployment %q was skipped for this environment", model),
					"change the agent's model or update AI_PROJECT_DEPLOYMENT_BINDINGS to select a deployment",
				)
			}
			return replacement, nil
		}
	}

	onDisk, err := promptModelInfrastructureOnDisk(projectRoot)
	if err != nil {
		return "", err
	}
	if onDisk {
		return model, nil
	}

	selected, ok, err := environmentDeploymentNames(env["AI_PROJECT_DEPLOYMENTS"])
	if err != nil {
		return "", err
	}
	if model == "" || !ok || containsDeploymentName(selected, model) {
		return model, nil
	}
	declared, err := usedProjectDeploymentNames(agentService, services, projectRoot, lookup)
	if err != nil {
		return "", err
	}
	if !containsDeploymentName(declared, model) {
		return model, nil
	}
	missing := slices.DeleteFunc(slices.Clone(declared), func(name string) bool {
		return containsDeploymentName(selected, name)
	})
	if len(missing) > 1 {
		return "", exterrors.Dependency(
			exterrors.CodeModelDeploymentNotFound,
			fmt.Sprintf(
				"model deployment %q is not created in this environment, and neither are %s, "+
					"so its replacement cannot be inferred",
				model, strings.Join(slices.DeleteFunc(missing, func(name string) bool {
					return strings.EqualFold(name, model)
				}), ", "),
			),
			"set the agent's model to the deployment to use, then run `azd deploy` again",
		)
	}
	fallback := strings.TrimSpace(env["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
	if fallback == "" {
		return "", exterrors.Dependency(
			exterrors.CodeModelDeploymentNotFound,
			fmt.Sprintf("model deployment %q was skipped for this environment", model),
			"run `azd ai project add` again to choose a deployment, or set the agent's model to an existing deployment",
		)
	}
	return fallback, nil
}

func promptModelDeploymentBinding(bindings map[string]string, model string) (string, bool, error) {
	var replacement string
	bound := false
	for name, target := range bindings {
		if !strings.EqualFold(name, model) {
			continue
		}
		if bound && !strings.EqualFold(replacement, target) {
			return "", false, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("AI_PROJECT_DEPLOYMENT_BINDINGS contains conflicting bindings for model %q", model),
				"keep a single binding for this deployment name, ignoring case",
			)
		}
		replacement, bound = target, true
	}
	return replacement, bound, nil
}

func containsDeploymentName(names []string, name string) bool {
	return slices.ContainsFunc(names, func(candidate string) bool {
		return strings.EqualFold(candidate, name)
	})
}

// environmentDeploymentNames lists the names in AI_PROJECT_DEPLOYMENTS. The
// second result is false only when the value is blank.
func environmentDeploymentNames(value string) ([]string, bool, error) {
	deployments, present, err := DecodeProjectDeploymentsEnv(value)
	if !present || err != nil {
		return nil, present, err
	}
	names := make([]string, 0, len(deployments))
	for _, deployment := range deployments {
		names = append(names, strings.TrimSpace(deployment.Name))
	}
	return names, true, nil
}

// DecodeProjectDeploymentsEnv decodes AI_PROJECT_DEPLOYMENTS, which holds JSON
// or environment-escaped JSON. The bool is false when the value is blank.
func DecodeProjectDeploymentsEnv(value string) ([]Deployment, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false, nil
	}
	if !json.Valid([]byte(value)) {
		var unescaped string
		if err := json.Unmarshal([]byte(`"`+value+`"`), &unescaped); err != nil {
			return nil, true, fmt.Errorf("decoding AI_PROJECT_DEPLOYMENTS: %w", err)
		}
		value = unescaped
	}
	if !strings.HasPrefix(value, "[") {
		return nil, true, fmt.Errorf("decoding AI_PROJECT_DEPLOYMENTS: expected a JSON array")
	}
	var deployments []Deployment
	if err := json.Unmarshal([]byte(value), &deployments); err != nil {
		return nil, true, fmt.Errorf("decoding AI_PROJECT_DEPLOYMENTS: %w", err)
	}
	return deployments, true, nil
}

// EncodeProjectDeploymentsEnv encodes deployments the way the Projects
// extension stores AI_PROJECT_DEPLOYMENTS.
func EncodeProjectDeploymentsEnv(deployments []Deployment) (string, error) {
	if deployments == nil {
		deployments = []Deployment{}
	}
	data, err := json.Marshal(deployments)
	if err != nil {
		return "", fmt.Errorf("encoding AI_PROJECT_DEPLOYMENTS: %w", err)
	}
	escaped := strings.ReplaceAll(string(data), "\\", "\\\\")
	return strings.ReplaceAll(escaped, "\"", "\\\""), nil
}

// usedProjectDeploymentNames lists the deployment names declared in azure.yaml
// on the azure.ai.project services the agent uses.
func usedProjectDeploymentNames(
	agentService *azdext.ServiceConfig,
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
	lookup func(string) string,
) ([]string, error) {
	var names []string
	for _, use := range agentService.GetUses() {
		service := services[use]
		if service.GetHost() != FoundryProjectHost {
			continue
		}
		props, err := ResolveServiceConfigProps(service, projectRoot)
		if err != nil {
			return nil, err
		}
		if props == nil {
			continue
		}
		deployments, _ := props.AsMap()["deployments"].([]any)
		for _, item := range deployments {
			entry, _ := item.(map[string]any)
			name, _ := entry["name"].(string)
			name, err = ExpandEnv(name, lookup)
			if err != nil {
				return nil, fmt.Errorf("expanding project deployment name: %w", err)
			}
			if name = strings.TrimSpace(name); name != "" && !containsDeploymentName(names, name) {
				names = append(names, name)
			}
		}
	}
	return names, nil
}
