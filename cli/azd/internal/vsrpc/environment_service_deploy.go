// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/wbreza/container/v4"
)

// DeployAsync is the server implementation of:
// ValueTask<Environment> DeployAsync(RequestContext, string, IObserver<ProgressMessage>, CancellationToken)
//
// While it is named simply `DeployAsync`, it behaves as if the user had run `azd provision` and `azd deploy`.
func (s *environmentService) DeployAsync(
	ctx context.Context, rc RequestContext, name string, observer IObserver[ProgressMessage],
) (*Environment, error) {
	session, err := s.server.validateSession(ctx, rc.Session)
	if err != nil {
		return nil, err
	}

	outputWriter := &lineWriter{
		next: &messageWriter{
			ctx:      ctx,
			observer: observer,
			messageTemplate: ProgressMessage{
				Kind:     MessageKind(Info),
				Severity: Info,
			},
		},
	}

	spinnerWriter := &lineWriter{
		trimLineEndings: true,
		next: &messageWriter{
			ctx:      ctx,
			observer: observer,
			messageTemplate: ProgressMessage{
				Kind:     MessageKind(Important),
				Severity: Info,
			},
		},
	}

	serverContainer, err := session.newContainer(rc)
	if err != nil {
		return nil, err
	}
	serverContainer.outWriter.AddWriter(outputWriter)
	serverContainer.spinnerWriter.AddWriter(spinnerWriter)

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
	deployFlags.All = true

	container.MustRegisterScoped(serverContainer.Container, func() internal.EnvFlag {
		return internal.EnvFlag{
			EnvironmentName: name,
		}
	})

	container.MustRegisterSingleton(serverContainer.Container, func() *cmd.ProvisionFlags {
		return provisionFlags
	})

	container.MustRegisterSingleton(serverContainer.Container, func() *cmd.DeployFlags {
		return deployFlags
	})

	container.MustRegisterSingleton(serverContainer.Container, func() []string {
		return []string{}
	})

	container.MustRegisterNamedTransient(serverContainer.Container, "provisionAction", cmd.NewProvisionAction)
	container.MustRegisterNamedTransient(serverContainer.Container, "deployAction", cmd.NewDeployAction)

	var c struct {
		deployAction    actions.Action `container:"name"`
		provisionAction actions.Action `container:"name"`
	}

	if err := serverContainer.Fill(ctx, &c); err != nil {
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

	if err := spinnerWriter.Flush(ctx); err != nil {
		return nil, err
	}

	return s.refreshEnvironmentAsync(ctx, serverContainer, name, observer)
}
