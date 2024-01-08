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

	session.sessionMu.Lock()
	defer session.sessionMu.Unlock()

	outputWriter := &lineWriter{
		next: &messageWriter{
			ctx:      ctx,
			observer: observer,
		},
	}

	session.outWriter.AddWriter(outputWriter)
	defer session.outWriter.RemoveWriter(outputWriter)

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

	session.container.MustRegisterScoped(func() internal.EnvFlag {
		return internal.EnvFlag{
			EnvironmentName: name,
		}
	})

	ioc.RegisterInstance[*cmd.ProvisionFlags](session.container, provisionFlags)
	ioc.RegisterInstance[*cmd.DeployFlags](session.container, deployFlags)
	ioc.RegisterInstance[[]string](session.container, []string{})

	session.container.MustRegisterNamedTransient("provisionAction", cmd.NewProvisionAction)
	session.container.MustRegisterNamedTransient("deployAction", cmd.NewDeployAction)

	var c struct {
		deployAction    actions.Action `container:"name"`
		provisionAction actions.Action `container:"name"`
	}

	if err := session.container.Fill(&c); err != nil {
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
