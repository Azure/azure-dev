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

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ResolvePromptAgentModel returns the deployment a prompt agent should use in
// the current environment.
//
// ${VAR} references are expanded first, as for tools and policies. Then, when
// adopting a template against an existing Foundry project, Projects records
// the deployments to create for this environment in AI_PROJECT_DEPLOYMENTS and
// the chosen default in AZURE_AI_MODEL_DEPLOYMENT_NAME; azure.yaml keeps the
// template's deployments. A model that names a declared deployment missing
// from that list was replaced by an existing deployment (or skipped), so the
// environment default is used instead.
func ResolvePromptAgentModel(
	model string,
	agentService *azdext.ServiceConfig,
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
	env map[string]string,
) (string, error) {
	expanded, err := ExpandEnv(strings.TrimSpace(model), func(name string) string {
		if value, ok := env[name]; ok {
			return value
		}
		value, _ := os.LookupEnv(name)
		return value
	})
	if err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to expand model %q: %s", model, err),
			"check the ${VAR} reference in the agent definition's model",
		)
	}
	model = strings.TrimSpace(expanded)

	selected, ok := environmentDeploymentNames(env["AI_PROJECT_DEPLOYMENTS"])
	if model == "" || !ok || slices.Contains(selected, model) {
		return model, nil
	}
	declared, err := usedProjectDeploymentNames(agentService, services, projectRoot)
	if err != nil {
		return "", err
	}
	if !slices.Contains(declared, model) {
		return model, nil
	}
	missing := slices.DeleteFunc(slices.Clone(declared), func(name string) bool {
		return slices.Contains(selected, name)
	})
	if len(missing) > 1 {
		return "", exterrors.Dependency(
			exterrors.CodeModelDeploymentNotFound,
			fmt.Sprintf(
				"model deployment %q is not created in this environment, and neither are %s, "+
					"so its replacement cannot be inferred",
				model, strings.Join(slices.DeleteFunc(missing, func(name string) bool { return name == model }), ", "),
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

// environmentDeploymentNames lists the names in AI_PROJECT_DEPLOYMENTS. The
// second result is false when the value is unset or unreadable, which leaves
// the model as is.
func environmentDeploymentNames(value string) ([]string, bool) {
	deployments, present, err := DecodeProjectDeploymentsEnv(value)
	if !present || err != nil {
		return nil, false
	}
	names := make([]string, 0, len(deployments))
	for _, deployment := range deployments {
		names = append(names, strings.TrimSpace(deployment.Name))
	}
	return names, true
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
			if name = strings.TrimSpace(name); name != "" && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names, nil
}
