// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"google.golang.org/protobuf/types/known/structpb"
)

const projectDeploymentsEnvKey = "AI_PROJECT_DEPLOYMENTS"

type projectServiceConfig struct {
	Endpoint    string                 `json:"endpoint,omitempty"`
	Deployments []synthesis.Deployment `json:"deployments,omitempty"`
}

func projectLifecycleHandler(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	args *azdext.ProjectEventArgs,
) error {
	return projectLifecycleHandlerWithOptions(ctx, azdClient, args, false)
}

// projectLifecycleHandlerBeforeProvision defers canonical deployment values
// until the provisioning provider has reconciled them for the active scope.
func projectLifecycleHandlerBeforeProvision(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	args *azdext.ProjectEventArgs,
) error {
	return projectLifecycleHandlerWithOptions(ctx, azdClient, args, true)
}

func projectLifecycleHandlerWithOptions(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	args *azdext.ProjectEventArgs,
	deferCanonicalDeployments bool,
) error {
	if args == nil || args.Project == nil {
		return fmt.Errorf("project lifecycle event has no project")
	}

	cfg, found, err := loadProjectServiceConfig(
		args.Project.Services,
		args.Project.Path,
	)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if deferCanonicalDeployments && hasCanonicalDeploymentReferences(cfg.Deployments) {
		return nil
	}

	current, err := azdClient.Environment().GetCurrent(
		ctx,
		&azdext.EmptyRequest{},
	)
	if err != nil {
		return fmt.Errorf("resolving current azd environment: %w", err)
	}
	if current.GetEnvironment().GetName() == "" {
		return fmt.Errorf("current azd environment has no name")
	}

	envResponse, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: current.GetEnvironment().GetName(),
	})
	if err != nil {
		return fmt.Errorf("reading current azd environment values: %w", err)
	}
	env := make(map[string]string, len(envResponse.GetKeyValues()))
	for _, value := range envResponse.GetKeyValues() {
		if value != nil {
			env[value.GetKey()] = value.GetValue()
		}
	}
	deployments, err := synthesis.ResolveDeployments(cfg.Deployments, env, false)
	if err != nil {
		return fmt.Errorf("resolving project model deployments: %w", err)
	}

	value, err := encodeProjectDeployments(deployments)
	if err != nil {
		return err
	}
	if _, err := azdClient.Environment().SetValue(
		ctx,
		&azdext.SetEnvRequest{
			EnvName: current.GetEnvironment().GetName(),
			Key:     projectDeploymentsEnvKey,
			Value:   value,
		},
	); err != nil {
		return fmt.Errorf(
			"setting %s in azd environment: %w",
			projectDeploymentsEnvKey,
			err,
		)
	}

	return nil
}

func hasCanonicalDeploymentReferences(
	deployments []synthesis.Deployment,
) bool {
	for _, deployment := range deployments {
		values := []string{
			deployment.Name,
			deployment.Model.Name,
			deployment.Model.Format,
			deployment.Model.Version,
			deployment.Sku.Name,
		}
		if capacity, ok := deployment.Sku.Capacity.(string); ok {
			values = append(values, capacity)
		}
		for _, value := range values {
			if isCanonicalDeploymentReference(value) {
				return true
			}
		}
	}
	return false
}

func isCanonicalDeploymentReference(value string) bool {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "${") ||
		!strings.HasSuffix(trimmed, "}") {
		return false
	}
	key := trimmed[2 : len(trimmed)-1]
	for _, base := range []string{
		"AZURE_AI_MODEL_DEPLOYMENT_NAME",
		"AZURE_AI_MODEL_NAME",
		"AZURE_AI_MODEL_FORMAT",
		"AZURE_AI_MODEL_VERSION",
		"AZURE_AI_MODEL_SKU_NAME",
		"AZURE_AI_MODEL_SKU_CAPACITY",
	} {
		if key == base {
			return true
		}
		suffix, ok := strings.CutPrefix(key, base+"_")
		if !ok || suffix == "" || suffix[0] == '0' {
			continue
		}
		index, err := strconv.Atoi(suffix)
		if err == nil && index >= 2 {
			return true
		}
	}
	return false
}

func loadProjectServiceConfig(
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
) (*projectServiceConfig, bool, error) {
	var names []string
	for name, service := range services {
		if service.GetHost() == aiProjectHost {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	switch len(names) {
	case 0:
		return nil, false, nil
	case 1:
	default:
		return nil, false, fmt.Errorf(
			"multiple services use host %q: %s",
			aiProjectHost,
			strings.Join(names, ", "),
		)
	}

	service := services[names[0]]
	props := projectServiceProperties(service)
	cfg := &projectServiceConfig{}
	if props == nil {
		return cfg, true, nil
	}

	values := props.AsMap()
	if projectRoot != "" {
		resolved, err := foundry.ResolveFileRefs(values, projectRoot)
		if err != nil {
			return nil, false, fmt.Errorf(
				"resolve project service %q $ref includes: %w",
				names[0],
				err,
			)
		}
		values = resolved
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil, false, fmt.Errorf(
			"encoding project service %q config: %w",
			names[0],
			err,
		)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, false, fmt.Errorf(
			"parsing project service %q config: %w",
			names[0],
			err,
		)
	}
	cfg.Deployments, err = synthesis.ResolveDeployments(cfg.Deployments, nil, true)
	if err != nil {
		return nil, false, fmt.Errorf(
			"normalizing project service %q deployments: %w",
			names[0],
			err,
		)
	}
	return cfg, true, nil
}

func projectServiceProperties(
	service *azdext.ServiceConfig,
) *structpb.Struct {
	if props := service.GetAdditionalProperties(); props != nil &&
		len(props.GetFields()) > 0 {
		return props
	}
	return service.GetConfig()
}

func encodeProjectDeployments(
	deployments []synthesis.Deployment,
) (string, error) {
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
