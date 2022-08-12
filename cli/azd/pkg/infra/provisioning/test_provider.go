package provisioning

import (
	"context"
	"errors"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
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

func (p *TestProvider) Preview(ctx context.Context) *async.InteractiveTaskWithProgress[*PreviewResult, *PreviewProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*PreviewResult, *PreviewProgress]) {
			asyncContext.SetProgress(&PreviewProgress{Message: "Preparing deployment", Timestamp: time.Now()})

			params := make(map[string]InputParameter)
			params["location"] = InputParameter{Value: p.env.Values["AZURE_LOCATION"]}

			previewResult := PreviewResult{
				Deployment: Deployment{
					Parameters: params,
					Outputs:    make(map[string]OutputParameter),
				},
			}

			asyncContext.SetProgress(&PreviewProgress{Message: "Deployment preparation completed", Timestamp: time.Now()})
			asyncContext.SetResult(&previewResult)
		})
}

func (p *TestProvider) GetDeployment(ctx context.Context, scope Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			asyncContext.SetProgress(&DeployProgress{
				Message:    "Looking up deployment",
				Operations: []azcli.AzCliResourceOperation{},
				Timestamp:  time.Now(),
			})

			deployment := Deployment{
				Parameters: make(map[string]InputParameter),
				Outputs:    make(map[string]OutputParameter),
			}

			deployResult := DeployResult{
				Operations: []azcli.AzCliResourceOperation{},
				Deployment: &deployment,
			}

			asyncContext.SetResult(&deployResult)
		})
}

// Provisioning the infrastructure within the specified template
func (p *TestProvider) Deploy(ctx context.Context, deployment *Deployment, scope Scope) *async.InteractiveTaskWithProgress[*DeployResult, *DeployProgress] {
	return async.RunInteractiveTaskWithProgress(
		func(asyncContext *async.InteractiveTaskContextWithProgress[*DeployResult, *DeployProgress]) {
			asyncContext.SetProgress(&DeployProgress{
				Message:    "Deploying azure resources",
				Operations: []azcli.AzCliResourceOperation{},
				Timestamp:  time.Now(),
			})

			deployment := Deployment{
				Parameters: make(map[string]InputParameter),
				Outputs:    make(map[string]OutputParameter),
			}

			deployResult := DeployResult{
				Operations: []azcli.AzCliResourceOperation{},
				Deployment: &deployment,
			}

			asyncContext.SetProgress(&DeployProgress{Operations: []azcli.AzCliResourceOperation{}, Timestamp: time.Now()})
			asyncContext.SetResult(&deployResult)
		})
}

func (p *TestProvider) Destroy(ctx context.Context, deployment *Deployment, options DestroyOptions) *async.InteractiveTaskWithProgress[*DestroyResult, *DestroyProgress] {
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

func NewTestProvider(env *environment.Environment, projectPath string, options Options, console input.Console) Provider {
	return &TestProvider{
		env:         env,
		projectPath: projectPath,
		options:     options,
		console:     console,
	}
}
