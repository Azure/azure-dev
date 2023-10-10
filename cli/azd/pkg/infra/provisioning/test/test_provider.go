// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package test contains an test implementation of provider.Provider. This
// provider is registered for use when this package is imported, and can be imported for
// side effects only to register the provider, e.g.:
package test

import (
	"context"
	"errors"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type TestProvider struct {
	envManager  environment.Manager
	env         *environment.Environment
	projectPath string
	options     Options
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

func (p *TestProvider) Initialize(ctx context.Context, projectPath string, options Options) error {
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
	return EnsureSubscriptionAndLocation(
		ctx,
		t.envManager,
		t.env,
		t.prompters,
		func(_ account.Location) bool { return true },
	)
}

func (p *TestProvider) State(ctx context.Context, options *StateOptions) (*StateResult, error) {
	// TODO: progress, "Looking up deployment"

	state := State{
		Outputs:   make(map[string]OutputParameter),
		Resources: make([]Resource, 0),
	}

	return &StateResult{
		State: &state,
	}, nil
}

func (p *TestProvider) GetDeployment(ctx context.Context) (*DeployResult, error) {
	// TODO: progress, "Looking up deployment"

	deployment := Deployment{
		Parameters: make(map[string]InputParameter),
		Outputs:    make(map[string]OutputParameter),
	}

	return &DeployResult{
		Deployment: &deployment,
	}, nil
}

// Provisioning the infrastructure within the specified template
func (p *TestProvider) Deploy(ctx context.Context) (*DeployResult, error) {
	// TODO: progress, "Deploying azure resources"

	deployment := Deployment{
		Parameters: make(map[string]InputParameter),
		Outputs:    make(map[string]OutputParameter),
	}

	return &DeployResult{
		Deployment: &deployment,
	}, nil
}

// Provisioning the infrastructure within the specified template
func (p *TestProvider) Preview(ctx context.Context) (*DeployPreviewResult, error) {
	return &DeployPreviewResult{
		Preview: &DeploymentPreview{
			Status:     "Completed",
			Properties: &DeploymentPreviewProperties{},
		},
	}, nil
}

func (p *TestProvider) Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	// TODO: progress, "Starting destroy"

	destroyResult := DestroyResult{
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

func NewTestProvider(
	envManager environment.Manager,
	env *environment.Environment,
	console input.Console,
	prompters prompt.Prompter,
) Provider {
	return &TestProvider{
		envManager: envManager,
		env:        env,
		console:    console,
		prompters:  prompters,
	}
}
