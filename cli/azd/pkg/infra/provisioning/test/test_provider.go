// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package test contains an test implementation of provider.Provider. This
// provider is registered for use when this package is imported, and can be imported for
// side effects only to register the provider, e.g.:
package test

import (
	"context"
	"errors"

	"github.com/azure/azure-dev/pkg/environment"
	"github.com/azure/azure-dev/pkg/infra/provisioning"
	"github.com/azure/azure-dev/pkg/input"
	"github.com/azure/azure-dev/pkg/prompt"
	"github.com/azure/azure-dev/pkg/tools"
)

type TestProvider struct {
	envManager  environment.Manager
	env         *environment.Environment
	projectPath string
	options     provisioning.Options
	console     input.Console
	prompters   prompt.Prompter
}

// Name gets the name of the infra provider
func (p *TestProvider) Name() string {
	return "Test"
}

func (p *TestProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (p *TestProvider) Initialize(ctx context.Context, projectPath string, options provisioning.Options) error {
	p.projectPath = projectPath
	p.options = options

	return p.EnsureEnv(ctx)
}

// EnsureEnv ensures that the environment is in a provision-ready state with required values set, prompting the user if
// values are unset.
//
// An environment is considered to be in a provision-ready state if it contains both an AZURE_SUBSCRIPTION_ID and
// AZURE_LOCATION value.
func (t *TestProvider) EnsureEnv(ctx context.Context) error {
	return provisioning.EnsureSubscriptionAndLocation(
		ctx,
		t.envManager,
		t.env,
		t.prompters,
		provisioning.EnsureSubscriptionAndLocationOptions{},
	)
}

func (p *TestProvider) State(ctx context.Context, options *provisioning.StateOptions) (*provisioning.StateResult, error) {
	// TODO: progress, "Looking up deployment"

	state := provisioning.State{
		Outputs:   make(map[string]provisioning.OutputParameter),
		Resources: make([]provisioning.Resource, 0),
	}

	return &provisioning.StateResult{
		State: &state,
	}, nil
}

func (p *TestProvider) GetDeployment(ctx context.Context) (*provisioning.DeployResult, error) {
	// TODO: progress, "Looking up deployment"

	deployment := provisioning.Deployment{
		Parameters: make(map[string]provisioning.InputParameter),
		Outputs:    make(map[string]provisioning.OutputParameter),
	}

	return &provisioning.DeployResult{
		Deployment: &deployment,
	}, nil
}

// Provisioning the infrastructure within the specified template
func (p *TestProvider) Deploy(ctx context.Context) (*provisioning.DeployResult, error) {
	// TODO: progress, "Deploying azure resources"

	deployment := provisioning.Deployment{
		Parameters: make(map[string]provisioning.InputParameter),
		Outputs:    make(map[string]provisioning.OutputParameter),
	}

	return &provisioning.DeployResult{
		Deployment: &deployment,
	}, nil
}

// Provisioning the infrastructure within the specified template
func (p *TestProvider) Preview(ctx context.Context) (*provisioning.DeployPreviewResult, error) {
	return &provisioning.DeployPreviewResult{
		Preview: &provisioning.DeploymentPreview{
			Status:     "Completed",
			Properties: &provisioning.DeploymentPreviewProperties{},
		},
	}, nil
}

func (p *TestProvider) Destroy(
	ctx context.Context,
	options provisioning.DestroyOptions,
) (*provisioning.DestroyResult, error) {
	// TODO: progress, "Starting destroy"

	destroyResult := provisioning.DestroyResult{
		InvalidatedEnvKeys: []string{},
	}

	confirmOptions := input.ConsoleOptions{Message: "Are you sure you want to destroy?"}
	confirmed, err := p.console.Confirm(ctx, confirmOptions)

	if err != nil {
		return nil, err
	}

	if !confirmed {
		return nil, errors.New("user denied confirmation")
	}

	return &destroyResult, nil
}

func (p *TestProvider) Parameters(ctx context.Context) ([]provisioning.Parameter, error) {
	// not supported (no-op)
	return nil, nil
}

func NewTestProvider(
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
	prompters prompt.Prompter,
) provisioning.Provider {
	return &TestProvider{
		envManager: envManager,
		env:        env,
		console:    console,
		prompters:  prompters,
	}
}
