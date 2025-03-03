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
// ValueTask<Environment> DeployAsync(RequestContext, string, IObserver<ProgressMessage>, CancellationToken)
//
// While it is named simply `DeployAsync`, it behaves as if the user had run `azd provision` and `azd deploy`.
func (s *environmentService) DeployAsync(
	ctx context.Context, rc RequestContext, name string, observer *Observer[ProgressMessage],
) (*Environment, error) {
	session, err := s.server.validateSession(rc.Session)
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

	container, err := session.newContainer(rc)
	if err != nil {
		return nil, err
	}
	container.outWriter.AddWriter(outputWriter)
	container.spinnerWriter.AddWriter(spinnerWriter)

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

	container.MustRegisterScoped(func() internal.EnvFlag {
		return internal.EnvFlag{
			EnvironmentName: name,
		}
	})

	ioc.RegisterInstance(container.NestedContainer, provisionFlags)
	ioc.RegisterInstance(container.NestedContainer, deployFlags)
	ioc.RegisterInstance(container.NestedContainer, []string{})

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

	if err := spinnerWriter.Flush(ctx); err != nil {
		return nil, err
	}

	return s.refreshEnvironmentAsync(ctx, container, name, observer)
}
