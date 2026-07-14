// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
	if args == nil || args.Project == nil {
		return fmt.Errorf("project lifecycle event has no project")
	}

	cfg, found, err := loadProjectServiceConfig(args.Project.Services)
	if err != nil {
		return err
	}
	if !found {
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

	value, err := encodeProjectDeployments(cfg.Deployments)
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

func loadProjectServiceConfig(
	services map[string]*azdext.ServiceConfig,
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

	data, err := json.Marshal(props.AsMap())
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
