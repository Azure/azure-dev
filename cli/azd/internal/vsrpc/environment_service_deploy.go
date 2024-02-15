// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

// DeployAsync is the server implementation of:
// ValueTask<Environment> DeployAsync(Session, string, IObserver<ProgressMessage>, CancellationToken)
//
// While it is named simply `DeployAsync`, it behaves as if the user had run `azd provision` and `azd deploy`.
func (s *environmentService) DeployAsync(
	ctx context.Context, sessionId Session, name string, observer IObserver[ProgressMessage],
) (*Environment, error) {
	session, err := s.server.validateSession(ctx, sessionId)
	if err != nil {
		return nil, err
	}

	outputWriter := &lineWriter{
		next: &messageWriter{
			ctx:      ctx,
			observer: observer,
		},
	}

	container, err := session.newContainer()
	if err != nil {
		return nil, err
	}
	container.outWriter.AddWriter(outputWriter)
	defer container.outWriter.RemoveWriter(outputWriter)

	provisionFlags := cmd.NewProvisionFlagsFromEnvAndOptions(
		&internal.EnvFlag{
			EnvironmentName: name,
		},
		&internal.GlobalCommandOptions{
			Cwd:      session.rootPath,
			NoPrompt: true,
		},
	)

	deployFlags := cmd.NewDeployFlagsFromEnvAndOptions(
		&internal.EnvFlag{
			EnvironmentName: name,
		},
		&internal.GlobalCommandOptions{
			Cwd:      session.rootPath,
			NoPrompt: true,
		},
	)

	container.MustRegisterScoped(func() internal.EnvFlag {
		return internal.EnvFlag{
			EnvironmentName: name,
		}
	})

	ioc.RegisterInstance[*cmd.ProvisionFlags](container.NestedContainer, provisionFlags)
	ioc.RegisterInstance[*cmd.DeployFlags](container.NestedContainer, deployFlags)
	ioc.RegisterInstance[[]string](container.NestedContainer, []string{})

	container.MustRegisterNamedTransient("provisionAction", cmd.NewProvisionAction)
	container.MustRegisterNamedTransient("deployAction", cmd.NewDeployAction)

	var c struct {
		deployAction    actions.Action `container:"name"`
		provisionAction actions.Action `container:"name"`
	}

	if err := container.Fill(&c); err != nil {
		return nil, err
	}

	if _, err := c.provisionAction.Run(ctx); err != nil {
		return nil, err
	}

	if _, err := c.deployAction.Run(ctx); err != nil {
		return nil, err
	}

	if err := outputWriter.Flush(ctx); err != nil {
		return nil, err
	}

	return s.refreshEnvironmentAsyncWithSession(ctx, session, name, observer)
}
