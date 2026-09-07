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

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type environmentPlan struct {
	Sets   map[string]string
	Unsets []string
}

// Registry endpoints and resource IDs are reset with project-bound state.
// Reusing them after a replacement must go through an explicit, validated
// registry-selection path rather than inheriting the old environment.
var projectReplacementEnvironmentKeys = []string{
	"AZURE_AI_MODEL_DEPLOYMENT_NAME",
	"AZURE_CONTAINER_REGISTRY_ENDPOINT",
	"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
	"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
	"AZD_FOUNDRY_ACR_MODE",
	"AZD_FOUNDRY_ACR_PULL_ASSIGNED",
	"AZURE_AI_PROJECT_CONNECTION_NAMES",
	"AZURE_FOUNDRY_RESOURCE_GROUP",
	"AZD_FOUNDRY_RESOURCE_GROUP_ID",
}

func projectIdentityChanged(
	oldValues map[string]string,
	oldEndpoint string,
	project *resolvedProject,
) bool {
	if project == nil {
		return false
	}

	oldEndpoint = strings.TrimSpace(oldEndpoint)
	if oldEndpoint == "" {
		oldEndpoint = strings.TrimSpace(oldValues["FOUNDRY_PROJECT_ENDPOINT"])
	}
	oldID := strings.TrimSpace(oldValues["AZURE_AI_PROJECT_ID"])

	if project.Mode == projectModeNew {
		for _, key := range []string{
			"AZURE_AI_PROJECT_ID",
			"AZURE_AI_ACCOUNT_NAME",
			"AZURE_AI_PROJECT_NAME",
			"FOUNDRY_PROJECT_ENDPOINT",
		} {
			if strings.TrimSpace(oldValues[key]) != "" {
				return true
			}
		}
		return oldEndpoint != ""
	}

	targetEndpoint := strings.TrimSpace(project.Endpoint)
	targetID := strings.TrimSpace(project.ResourceId)
	if oldEndpoint != "" && targetEndpoint != "" &&
		!equalProjectEndpoint(oldEndpoint, targetEndpoint) {
		return true
	}
	if oldID != "" && targetID != "" &&
		!strings.EqualFold(oldID, targetID) {
		return true
	}
	if oldEndpoint == "" && oldID != "" && targetEndpoint != "" {
		oldProject, err := projectFromResourceID(oldID)
		if err != nil ||
			!equalProjectEndpoint(oldProject.Endpoint, targetEndpoint) {
			return true
		}
	}
	if oldID == "" && oldEndpoint != "" && targetID != "" {
		if targetProject, err := projectFromResourceID(targetID); err == nil &&
			!equalProjectEndpoint(oldEndpoint, targetProject.Endpoint) {
			return true
		}
	}
	if strings.TrimSpace(oldValues["AZURE_AI_ACCOUNT_NAME"]) != "" &&
		project.AccountName != "" &&
		!strings.EqualFold(
			strings.TrimSpace(oldValues["AZURE_AI_ACCOUNT_NAME"]),
			strings.TrimSpace(project.AccountName),
		) {
		return true
	}
	if strings.TrimSpace(oldValues["AZURE_AI_PROJECT_NAME"]) != "" &&
		project.ProjectName != "" &&
		!strings.EqualFold(
			strings.TrimSpace(oldValues["AZURE_AI_PROJECT_NAME"]),
			strings.TrimSpace(project.ProjectName),
		) {
		return true
	}
	return false
}

// planProjectEnvironment calculates environment mutations.
// It is independent from gRPC for daemon-free tests.
func planProjectEnvironment(
	oldValues map[string]string,
	mode projectMode,
	project *resolvedProject,
	projectReplaced bool,
) environmentPlan {
	sets := map[string]string{}
	if project == nil {
		project = &resolvedProject{}
	}
	if project.SubscriptionId != "" {
		sets["AZURE_SUBSCRIPTION_ID"] = project.SubscriptionId
	}
	if project.UserTenantId != "" {
		sets["AZURE_TENANT_ID"] = project.UserTenantId
	}

	switch mode {
	case projectModeExistingID:
		for key, value := range map[string]string{
			"AZURE_AI_PROJECT_ID":                           project.ResourceId,
			"AZURE_RESOURCE_GROUP":                          project.ResourceGroupName,
			"AZURE_AI_ACCOUNT_NAME":                         project.AccountName,
			"AZURE_AI_PROJECT_NAME":                         project.ProjectName,
			"FOUNDRY_PROJECT_ENDPOINT":                      project.Endpoint,
			"AZURE_OPENAI_ENDPOINT":                         project.OpenAIEndpoint,
			"AZURE_AI_DEPLOYMENTS_LOCATION":                 project.Location,
			"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT": project.Endpoint,
			"USE_EXISTING_AI_PROJECT":                       "true",
		} {
			if value != "" {
				sets[key] = value
			}
		}
		// Do not overwrite a preselected location when ARM omits one.
		if project.Location != "" && oldValues["AZURE_LOCATION"] == "" {
			sets["AZURE_LOCATION"] = project.Location
		}
	case projectModeExistingEndpoint:
		for key, value := range map[string]string{
			"AZURE_AI_PROJECT_NAME":                         project.ProjectName,
			"FOUNDRY_PROJECT_ENDPOINT":                      project.Endpoint,
			"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT": project.Endpoint,
			"USE_EXISTING_AI_PROJECT":                       "true",
		} {
			if value != "" {
				sets[key] = value
			}
		}
	case projectModeNew:
		sets["USE_EXISTING_AI_PROJECT"] = "false"
		if project.Location != "" {
			sets["AZURE_LOCATION"] = project.Location
			sets["AZURE_AI_DEPLOYMENTS_LOCATION"] = project.Location
		}
	}

	deleteKeys := map[string]struct{}{}
	switch mode {
	case projectModeNew:
		for _, key := range []string{
			"AZURE_AI_PROJECT_ID", "AZURE_RESOURCE_GROUP", "AZURE_AI_ACCOUNT_NAME",
			"AZURE_AI_PROJECT_NAME", "FOUNDRY_PROJECT_ENDPOINT",
			"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT", "AZURE_OPENAI_ENDPOINT",
			"AZURE_AI_DEPLOYMENTS_LOCATION",
		} {
			deleteKeys[key] = struct{}{}
		}
	case projectModeExistingEndpoint:
		for _, key := range []string{
			"AZURE_AI_PROJECT_ID", "AZURE_RESOURCE_GROUP", "AZURE_AI_ACCOUNT_NAME",
			"AZURE_OPENAI_ENDPOINT", "AZURE_AI_DEPLOYMENTS_LOCATION",
		} {
			deleteKeys[key] = struct{}{}
		}
	}
	if projectReplaced {
		for _, key := range projectReplacementEnvironmentKeys {
			deleteKeys[key] = struct{}{}
		}
	}
	for key := range sets {
		delete(deleteKeys, key)
	}
	unsets := make([]string, 0, len(deleteKeys))
	for key := range deleteKeys {
		if value, exists := oldValues[key]; exists && value != "" {
			unsets = append(unsets, key)
		}
	}
	slices.Sort(unsets)
	return environmentPlan{Sets: sets, Unsets: unsets}
}

func applyProjectEnvironmentPlan(
	oldValues map[string]string,
	plan environmentPlan,
) map[string]string {
	effective := make(map[string]string, len(oldValues)+len(plan.Sets))
	for key, value := range oldValues {
		effective[key] = value
	}
	for key, value := range plan.Sets {
		effective[key] = value
	}
	for _, key := range plan.Unsets {
		delete(effective, key)
	}
	return effective
}

func reconcileProjectEnvironmentWithRollback(
	ctx context.Context,
	client *azdext.AzdClient,
	envName string,
	mode projectMode,
	project *resolvedProject,
	projectReplaced bool,
) (func() error, map[string]string, error) {
	response, err := client.Environment().GetValues(ctx,
		&azdext.GetEnvironmentRequest{Name: envName})
	if err != nil {
		return func() error { return nil }, nil, exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("read project environment %q: %s", envName, err),
			"select or create an azd environment before initializing a project",
		)
	}
	old := map[string]string{}
	for _, pair := range response.GetKeyValues() {
		if pair != nil {
			old[pair.GetKey()] = pair.GetValue()
		}
	}
	plan := planProjectEnvironment(old, mode, project, projectReplaced)
	keys := make([]string, 0, len(plan.Sets))
	for key := range plan.Sets {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName,
			Key:     key,
			Value:   plan.Sets[key],
		}); err != nil {
			operationErr := fmt.Errorf(
				"set project environment value %s: %w", key, err,
			)
			return func() error { return nil }, nil,
				rollbackProjectEnvironment(
					ctx, client, envName, old, plan, operationErr,
				)
		}
	}
	for _, key := range plan.Unsets {
		if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName,
			Key:     key,
			Value:   "",
		}); err != nil {
			operationErr := fmt.Errorf(
				"clear project environment value %s: %w", key, err,
			)
			return func() error { return nil }, nil,
				rollbackProjectEnvironment(
					ctx, client, envName, old, plan, operationErr,
				)
		}
	}
	return func() error {
		return withProjectRollbackContext(ctx, func(rollbackCtx context.Context) error {
			return restoreProjectEnvironment(
				rollbackCtx,
				client,
				envName,
				old,
				plan,
			)
		})
	}, applyProjectEnvironmentPlan(old, plan), nil
}

func rollbackProjectEnvironment(
	ctx context.Context,
	client *azdext.AzdClient,
	envName string,
	old map[string]string,
	plan environmentPlan,
	operationErr error,
) error {
	if restoreErr := withProjectRollbackContext(
		ctx,
		func(rollbackCtx context.Context) error {
			return restoreProjectEnvironment(
				rollbackCtx,
				client,
				envName,
				old,
				plan,
			)
		},
	); restoreErr != nil {
		return errors.Join(operationErr, restoreErr)
	}
	return operationErr
}

func restoreProjectEnvironment(
	ctx context.Context,
	client *azdext.AzdClient,
	envName string,
	old map[string]string,
	plan environmentPlan,
) error {
	keys := make([]string, 0, len(plan.Sets)+len(plan.Unsets))
	seen := map[string]struct{}{}
	for key := range plan.Sets {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, key := range plan.Unsets {
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	var restoreErrs []error
	for _, key := range keys {
		value := ""
		if oldValue, exists := old[key]; exists {
			value = oldValue
		}
		if _, err := client.Environment().SetValue(
			ctx,
			&azdext.SetEnvRequest{
				EnvName: envName,
				Key:     key,
				Value:   value,
			},
		); err != nil {
			restoreErrs = append(
				restoreErrs,
				fmt.Errorf("restore project environment value %s: %w", key, err),
			)
		}
	}
	return errors.Join(restoreErrs...)
}

func currentProjectEnvironment(
	ctx context.Context,
	client *azdext.AzdClient,
	envName string,
) (map[string]string, error) {
	response, err := client.Environment().GetValues(ctx,
		&azdext.GetEnvironmentRequest{Name: envName})
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(response.GetKeyValues()))
	for _, pair := range response.GetKeyValues() {
		if pair != nil {
			values[pair.GetKey()] = pair.GetValue()
		}
	}
	return values, nil
}

func resolveProjectEnvironmentName(
	ctx context.Context,
	client *azdext.AzdClient,
	explicit string,
	projectRoot string,
) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		if _, err := client.Environment().Select(ctx,
			&azdext.SelectEnvironmentRequest{Name: explicit}); err != nil {
			return "", fmt.Errorf("select environment %q: %w", explicit, err)
		}
		return explicit, nil
	}
	if response, err := client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); err == nil &&
		response.GetEnvironment() != nil && response.GetEnvironment().GetName() != "" {
		return response.GetEnvironment().GetName(), nil
	}
	name := deriveProjectEnvironmentName(projectRoot)
	if _, err := client.Environment().Select(ctx,
		&azdext.SelectEnvironmentRequest{Name: name}); err != nil {
		return "", exterrors.Dependency(
			exterrors.CodeEnvironmentNotFound,
			fmt.Sprintf("select environment %q: %s", name, err),
			"run `azd env new` or pass --environment with an existing environment",
		)
	}
	return name, nil
}

func deriveProjectEnvironmentName(projectRoot string) string {
	base := projectRoot
	if base == "" {
		base = "project"
	}
	if index := strings.LastIndexAny(base, `/\`); index >= 0 {
		base = base[index+1:]
	}
	base = strings.ToLower(base)
	var builder strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('-')
		}
	}
	name := strings.Trim(builder.String(), "-")
	if name == "" {
		name = "project"
	}
	if len(name) > 59 {
		name = strings.TrimRight(name[:59], "-")
	}
	return name + "-dev"
}
