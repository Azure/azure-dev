// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const projectDeploymentsEnvKey = "AI_PROJECT_DEPLOYMENTS"

// adoptedDeploymentChooser asks which template deployment the existing
// deployment replaces.
type adoptedDeploymentChooser func(existing string, declared []string) (string, error)

// replaceAdoptedTemplateDeployment drops the template deployment that
// --model-deployment replaces from this environment's AI_PROJECT_DEPLOYMENTS,
// so provisioning does not create it. azure.yaml is not changed.
func replaceAdoptedTemplateDeployment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	existing string,
	noPrompt bool,
) error {
	response, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("reading adopted project deployments: %w", err)
	}
	values := map[string]string{}
	envResponse, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: envName})
	if err != nil {
		return fmt.Errorf("reading environment for adopted deployments: %w", err)
	}
	for _, kv := range envResponse.GetKeyValues() {
		values[kv.GetKey()] = kv.GetValue()
	}
	declared, err := adoptedTemplateDeployments(
		response.GetProject().GetServices(), response.GetProject().GetPath(), values,
	)
	if err != nil {
		return err
	}
	choose := func(existing string, names []string) (string, error) {
		choices := make([]*azdext.SelectChoice, 0, len(names))
		for _, name := range names {
			choices = append(choices, &azdext.SelectChoice{Value: name, Label: name})
		}
		selected, err := azdClient.Prompt().Select(ctx, &azdext.SelectRequest{
			Options: &azdext.SelectOptions{
				Message: fmt.Sprintf("Which template deployment does %q replace?", existing),
				Choices: choices,
			},
		})
		if err != nil {
			return "", err
		}
		return names[selected.GetValue()], nil
	}
	value, write, err := planAdoptedDeploymentReplacement(
		declared, values[projectDeploymentsEnvKey], existing, noPrompt, choose,
	)
	if err != nil {
		return err
	}
	updates := []struct{ key, value string }{
		{"AZURE_AI_MODEL_DEPLOYMENT_NAME", existing},
	}
	if write {
		updates = append(updates, struct{ key, value string }{projectDeploymentsEnvKey, value})
	}
	for i, update := range updates {
		if err := setEnvValue(ctx, azdClient, envName, update.key, update.value); err != nil {
			rollbackCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx), projectWorkflowRollbackTimeout,
			)
			defer cancel()
			restoreErrs := []error{err}
			for j := i; j >= 0; j-- {
				restoreErrs = append(restoreErrs, setEnvValue(
					rollbackCtx, azdClient, envName, updates[j].key, values[updates[j].key],
				))
			}
			return errors.Join(restoreErrs...)
		}
	}
	return nil
}

// planAdoptedDeploymentReplacement returns the AI_PROJECT_DEPLOYMENTS value
// without the template deployment that existing replaces. The environment's
// list is kept when set; otherwise the template list is the starting point.
func planAdoptedDeploymentReplacement(
	declared []project.Deployment,
	envValue string,
	existing string,
	noPrompt bool,
	choose adoptedDeploymentChooser,
) (string, bool, error) {
	if len(declared) == 0 {
		return "", false, nil
	}
	current, present, err := project.DecodeProjectDeploymentsEnv(envValue)
	if err != nil {
		return "", false, err
	}
	if !present {
		current = declared
	}

	replaced := declared[0].Name
	if len(declared) > 1 {
		names := make([]string, 0, len(declared))
		for _, deployment := range declared {
			names = append(names, deployment.Name)
		}
		if noPrompt {
			return "", false, exterrors.Validation(
				exterrors.CodeConflictingArguments,
				fmt.Sprintf(
					"--model-deployment %q cannot tell which template deployment it replaces (%s)",
					existing, strings.Join(names, ", "),
				),
				"run `azd ai agent init` without --no-prompt to choose",
			)
		}
		replaced, err = choose(existing, names)
		if err != nil {
			return "", false, err
		}
	}

	remaining := slices.DeleteFunc(slices.Clone(current), func(deployment project.Deployment) bool {
		return strings.EqualFold(deployment.Name, replaced)
	})
	value, err := project.EncodeProjectDeploymentsEnv(remaining)
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// adoptedTemplateDeployments returns the deployments declared on the adopted
// azure.ai.project service, with ${VAR} references expanded from env.
func adoptedTemplateDeployments(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
	env map[string]string,
) ([]project.Deployment, error) {
	names := make([]string, 0, 1)
	for name, service := range services {
		if service.GetHost() == project.FoundryProjectHost {
			names = append(names, name)
		}
	}
	if len(names) != 1 {
		return nil, nil
	}
	props, err := project.ResolveServiceConfigProps(services[names[0]], projectRoot)
	if err != nil || props == nil {
		return nil, err
	}
	raw, _ := props.AsMap()["deployments"].([]any)
	lookup := func(name string) string { return env[name] }
	deployments := make([]project.Deployment, 0, len(raw))
	for i, item := range raw {
		expanded, err := expandDeploymentValue(item, lookup)
		if err != nil {
			return nil, fmt.Errorf("expanding deployment %d: %w", i, err)
		}
		if entry, ok := expanded.(map[string]any); ok {
			if sku, ok := entry["sku"].(map[string]any); ok {
				if capacity, ok := sku["capacity"].(string); ok {
					if sku["capacity"], err = strconv.Atoi(strings.TrimSpace(capacity)); err != nil {
						return nil, fmt.Errorf("deployment %d sku.capacity: %w", i, err)
					}
				}
			}
		}
		data, err := json.Marshal(expanded)
		if err != nil {
			return nil, err
		}
		var deployment project.Deployment
		if err := json.Unmarshal(data, &deployment); err != nil {
			return nil, fmt.Errorf("reading deployment %d: %w", i, err)
		}
		deployments = append(deployments, deployment)
	}
	return deployments, nil
}

func expandDeploymentValue(value any, lookup func(string) string) (any, error) {
	switch typed := value.(type) {
	case string:
		return project.ExpandEnv(typed, lookup)
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			expanded, err := expandDeploymentValue(item, lookup)
			if err != nil {
				return nil, err
			}
			result[key] = expanded
		}
		return result, nil
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			expanded, err := expandDeploymentValue(item, lookup)
			if err != nil {
				return nil, err
			}
			result = append(result, expanded)
		}
		return result, nil
	default:
		return value, nil
	}
}
