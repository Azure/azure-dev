// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package test contains an test implementation of provider.Provider. This
// provider is registered for use when this package is imported, and can be imported for
// side effects only to register the provider, e.g.:
//
// require(
//
//	_ "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/test"
//
// )
package test

import (
	"context"
	"errors"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

type TestProvider struct {
	env         *environment.Environment
	projectPath string
	options     Options
	console     input.Console
}

// Name gets the name of the infra provider
func (p *TestProvider) Name() string {
	return "Test"
}

func (p *TestProvider) RequiredExternalTools() []tools.ExternalTool {
	return []tools.ExternalTool{}
}

func (p *TestProvider) Plan(
	ctx context.Context,
) *async.InteractiveTaskWithProgress[*DeploymentPlan, *DeploymentPlanningProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeploymentPlan, *DeploymentPlanningProgress]) {
			asyncContext.SetProgress(&DeploymentPlanningProgress{Message: "Planning deployment", Timestamp: time.Now()})

			params := make(map[string]InputParameter)
			params["location"] = InputParameter{Value: p.env.Values["AZURE_LOCATION"]}

			deploymentPlan := DeploymentPlan{
				Deployment: Deployment{
					Parameters: params,
					Outputs:    make(map[string]OutputParameter),
				},
			}

			asyncContext.SetProgress(
				&DeploymentPlanningProgress{Message: "Deployment planning completed", Timestamp: time.Now()},
			)
			asyncContext.SetResult(&deploymentPlan)
		})
}

func (p *TestProvider) State(
	ctx context.Context,
	scope infra.Scope,
) *async.InteractiveTaskWithProgress[*StateResult, *StateProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*StateResult, *StateProgress]) {
			asyncContext.SetProgress(&StateProgress{
				Message:   "Looking up deployment",
				Timestamp: time.Now(),
			})

			state := State{
				Outputs:   make(map[string]OutputParameter),
				Resources: make([]Resource, 0),
			}

			stateResult := StateResult{
				State: &state,
			}

			asyncContext.SetResult(&stateResult)
		})
}

func (p *TestProvider) GetDeployment(
	ctx context.Context,
	scope infra.Scope,
) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			asyncContext.SetProgress(&DeployProgress{
				Message:   "Looking up deployment",
				Timestamp: time.Now(),
			})

			deployment := Deployment{
				Parameters: make(map[string]InputParameter),
				Outputs:    make(map[string]OutputParameter),
			}

			deployResult := DeployResult{
				Deployment: &deployment,
			}

			asyncContext.SetResult(&deployResult)
		})
}

// Provisioning the infrastructure within the specified template
func (p *TestProvider) Deploy(
	ctx context.Context,
	pd *DeploymentPlan,
	scope infra.Scope,
) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			asyncContext.SetProgress(&DeployProgress{
				Message:   "Deploying azure resources",
				Timestamp: time.Now(),
			})

			deployment := Deployment{
				Parameters: make(map[string]InputParameter),
				Outputs:    make(map[string]OutputParameter),
			}

			deployResult := DeployResult{
				Deployment: &deployment,
			}

			asyncContext.SetResult(&deployResult)
		})
}

func (p *TestProvider) Destroy(
	ctx context.Context,
	deployment *Deployment,
	options DestroyOptions,
) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DestroyResult, *DestroyProgress]) {
			asyncContext.SetProgress(&DestroyProgress{
				Message:   "Starting destroy",
				Timestamp: time.Now(),
			})

			destroyResult := DestroyResult{
				Resources: []azcli.AzCliResource{},
				Outputs:   deployment.Outputs,
			}

			err := asyncContext.Interact(func() error {
				confirmOptions := input.ConsoleOptions{Message: "Are you sure you want to destroy?"}
				confirmed, err := p.console.Confirm(ctx, confirmOptions)

				if err != nil {
					return err
				}

				if !confirmed {
					return errors.New("user denied confirmation")
				}

				return nil
			})

			if err != nil {
				asyncContext.SetError(err)
				return
			}

			asyncContext.SetProgress(&DestroyProgress{Message: "Finishing destroy", Timestamp: time.Now()})

			asyncContext.SetResult(&destroyResult)
		})
}

func NewTestProvider(env *environment.Environment, projectPath string, console input.Console, options Options) Provider {
	return &TestProvider{
		env:         env,
		projectPath: projectPath,
		options:     options,
		console:     console,
	}
}

// Registers the Test provider with the provisioning module
func init() {
	err := RegisterProvider(
		Test,
		func(
			ctx context.Context,
			env *environment.Environment,
			projectPath string,
			options Options,
			console input.Console,
			_ azcli.AzCli,
			_ exec.CommandRunner,
		) (Provider, error) {
			return NewTestProvider(env, projectPath, console, options), nil
		},
	)

	if err != nil {
		panic(err)
	}
}
