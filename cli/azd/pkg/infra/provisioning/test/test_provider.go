// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package test contains an test implementation of provider.Provider. This
// provider is registered for use when this package is imported, and can be imported for
// side effects only to register the provider, e.g.:
package test

import (
	"context"
	"errors"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type TestProvider struct {
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

	return p.prompters.EnsureEnv(ctx)
}

func (p *TestProvider) Plan(ctx context.Context) (*DeploymentPlan, error) {
	// TODO: progress "Planning deployment"

	params := make(map[string]InputParameter)
	params["location"] = InputParameter{Value: p.env.GetLocation()}

	return &DeploymentPlan{
		Deployment: Deployment{
			Parameters: params,
			Outputs:    make(map[string]OutputParameter),
		},
	}, nil
}

func (p *TestProvider) State(ctx context.Context) (*StateResult, error) {
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
func (p *TestProvider) Deploy(ctx context.Context, pd *DeploymentPlan) (*DeployResult, error) {
	// TODO: progress, "Deploying azure resources"

	deployment := Deployment{
		Parameters: make(map[string]InputParameter),
		Outputs:    make(map[string]OutputParameter),
	}

	return &DeployResult{
		Deployment: &deployment,
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
	env *environment.Environment,
	console input.Console,
	prompters prompt.Prompter,
) Provider {
	return &TestProvider{
		env:       env,
		console:   console,
		prompters: prompters,
	}
}
