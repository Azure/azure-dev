// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"
	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

const projectDeploymentsEnvKey = "AI_PROJECT_DEPLOYMENTS"

// adoptedDeploymentChooser asks which template deployment the existing
// deployment replaces.
type adoptedDeploymentChooser func(existing string, declared []string) (string, error)

func readAdoptedDeploymentState(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
) ([]project.Deployment, map[string]string, error) {
	response, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, nil, fmt.Errorf("reading adopted project deployments: %w", err)
	}
	values := map[string]string{}
	envResponse, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: envName})
	if err != nil {
		return nil, nil, fmt.Errorf("reading environment for adopted deployments: %w", err)
	}
	for _, kv := range envResponse.GetKeyValues() {
		values[kv.GetKey()] = kv.GetValue()
	}
	declared, err := adoptedTemplateDeployments(
		response.GetProject().GetServices(), response.GetProject().GetPath(), values,
	)
	return declared, values, err
}

func preflightAdoptedDeploymentReplacement(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	projectRoot string,
	flags *initFlags,
) (string, error) {
	existing := strings.TrimSpace(flags.modelDeployment)
	if existing == "" {
		return "", nil
	}
	declared, err := readStagedAdoptedDeployments(projectRoot)
	if err != nil {
		return "", err
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
			if exterrors.IsCancellation(err) {
				return "", exterrors.Cancelled("initialization with --model-deployment was cancelled")
			}
			return "", exterrors.FromPrompt(err, "failed to select the template deployment for --model-deployment")
		}
		return names[selected.GetValue()], nil
	}
	return selectAdoptedDeploymentReplacement(declared, existing, flags.noPrompt, choose)
}

func readStagedAdoptedDeployments(projectRoot string) ([]project.Deployment, error) {
	content, err := os.ReadFile(filepath.Join(projectRoot, "azure.yaml"))
	if err != nil {
		return nil, fmt.Errorf("reading staged azure.yaml: %w", err)
	}
	var document struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("parsing staged azure.yaml: %w", err)
	}
	services := map[string]*azdext.ServiceConfig{}
	for name, service := range document.Services {
		host, _ := service["host"].(string)
		if host != project.FoundryProjectHost {
			continue
		}
		props, err := structpb.NewStruct(service)
		if err != nil {
			return nil, fmt.Errorf("encoding project service %q: %w", name, err)
		}
		services[name] = &azdext.ServiceConfig{
			Name: name, Host: host, AdditionalProperties: props,
		}
	}
	raw, err := adoptedTemplateDeploymentEntries(services, projectRoot)
	if err != nil {
		return nil, err
	}
	declared := make([]project.Deployment, 0, len(raw))
	for i, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("template deployment %d must be an object", i)
		}
		name, ok := entry["name"].(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("template deployment %d must have a non-empty string name", i)
		}
		// Resolve names against the final environment when applying.
		declared = append(declared, project.Deployment{Name: name})
	}
	return declared, nil
}

// replaceAdoptedTemplateDeployment applies the preselected replacement using
// current environment values. azure.yaml is not changed and no prompt is shown.
func replaceAdoptedTemplateDeployment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	existing string,
	replaced string,
) error {
	declared, values, err := readAdoptedDeploymentState(ctx, azdClient, envName)
	if err != nil {
		return err
	}
	replaced, err = project.ExpandEnv(replaced, adoptedDeploymentEnvironmentLookup(values))
	if err != nil {
		return fmt.Errorf("expanding adopted deployment name: %w", err)
	}
	value, write, err := planAdoptedDeploymentReplacement(
		declared, values[projectDeploymentsEnvKey], replaced,
	)
	if err != nil {
		return err
	}
	updates := []struct{ key, value string }{
		{"AZURE_AI_MODEL_DEPLOYMENT_NAME", existing},
	}
	if write {
		updates = append(updates, struct{ key, value string }{projectDeploymentsEnvKey, value})
		bindings, err := synthesis.DecodeDeploymentBindings(values[synthesis.DeploymentBindingsEnvKey])
		if err != nil {
			return err
		}
		synthesis.SetDeploymentBinding(bindings, replaced, existing)
		encoded, err := synthesis.EncodeDeploymentBindings(bindings)
		if err != nil {
			return err
		}
		updates = append(updates, struct{ key, value string }{synthesis.DeploymentBindingsEnvKey, encoded})
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
// without the preselected template deployment. The environment's
// list is kept when set; otherwise the template list is the starting point.
func planAdoptedDeploymentReplacement(
	declared []project.Deployment,
	envValue string,
	replaced string,
) (string, bool, error) {
	if replaced == "" {
		return "", false, nil
	}
	if !slices.ContainsFunc(declared, func(deployment project.Deployment) bool {
		return strings.EqualFold(deployment.Name, replaced)
	}) {
		return "", false, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("template deployment %q selected for --model-deployment is no longer declared", replaced),
			"retry initialization using the same template revision",
		)
	}
	current, present, err := project.DecodeProjectDeploymentsEnv(envValue)
	if err != nil {
		return "", false, err
	}
	if !present {
		current = declared
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

func selectAdoptedDeploymentReplacement(
	declared []project.Deployment,
	existing string,
	noPrompt bool,
	choose adoptedDeploymentChooser,
) (string, error) {
	if len(declared) == 0 {
		return "", nil
	}
	if len(declared) > 1 {
		names := make([]string, 0, len(declared))
		for _, deployment := range declared {
			names = append(names, deployment.Name)
		}
		if noPrompt {
			return "", exterrors.Validation(
				exterrors.CodeConflictingArguments,
				fmt.Sprintf(
					"--model-deployment %q cannot tell which template deployment it replaces (%s)",
					existing, strings.Join(names, ", "),
				),
				"run `azd ai agent init` without --no-prompt to choose",
			)
		}
		return choose(existing, names)
	}
	return declared[0].Name, nil
}

// adoptedTemplateDeployments returns the deployments declared on the adopted
// azure.ai.project service, with ${VAR} references expanded from env.
func adoptedTemplateDeployments(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
	env map[string]string,
) ([]project.Deployment, error) {
	raw, err := adoptedTemplateDeploymentEntries(services, projectRoot)
	if err != nil {
		return nil, err
	}
	lookup := adoptedDeploymentEnvironmentLookup(env)
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

func adoptedTemplateDeploymentEntries(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) ([]any, error) {
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
	return raw, nil
}

func adoptedDeploymentEnvironmentLookup(env map[string]string) func(string) string {
	return func(name string) string {
		if value, ok := env[name]; ok {
			return value
		}
		value, _ := os.LookupEnv(name)
		return value
	}
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
