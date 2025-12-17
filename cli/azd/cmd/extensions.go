// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// bindExtension binds the extension to the root command
func bindExtension(
	root *actions.ActionDescriptor,
	extension *extensions.Extension,
) error {
	// Split the namespace by dots to support nested namespaces
	namespaceParts := strings.Split(extension.Namespace, ".")

	// Start with the root command
	current := root

	// For each part except the last one, create or find a command in the hierarchy
	for i := 0; i < len(namespaceParts)-1; i++ {
		part := namespaceParts[i]

		// Check if a command with this name already exists
		found := false
		for _, child := range current.Children() {
			if child.Name == part {
				current = child
				found = true
				break
			}
		}

		// If not found, create a new command
		if !found {
			cmd := &cobra.Command{
				Use:   part,
				Short: extension.Description,
			}

			current = current.Add(part, &actions.ActionDescriptorOptions{
				Command: cmd,
				GroupingOptions: actions.CommandGroupOptions{
					RootLevelHelp: actions.CmdGroupExtensions,
				},
			})
		}
	}

	// The last part of the namespace is the actual command
	lastPart := namespaceParts[len(namespaceParts)-1]

	cmd := &cobra.Command{
		Use:                lastPart,
		Short:              extension.Description,
		Long:               extension.Description,
		DisableFlagParsing: true,
		// Add extension metadata as annotations for faster lookup later during invocation.
		Annotations: map[string]string{
			"extension.id":        extension.Id,
			"extension.namespace": extension.Namespace,
		},
	}

	current.Add(lastPart, &actions.ActionDescriptorOptions{
		Command:                cmd,
		ActionResolver:         newExtensionAction,
		DisableTroubleshooting: true,
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupExtensions,
		},
	})

	return nil
}

type extensionAction struct {
	console          input.Console
	extensionRunner  *extensions.Runner
	lazyEnv          *lazy.Lazy[*environment.Environment]
	extensionManager *extensions.Manager
	azdServer        *grpcserver.Server
	cmd              *cobra.Command
	args             []string
}

func newExtensionAction(
	console input.Console,
	extensionRunner *extensions.Runner,
	commandRunner exec.CommandRunner,
	lazyEnv *lazy.Lazy[*environment.Environment],
	extensionManager *extensions.Manager,
	cmd *cobra.Command,
	azdServer *grpcserver.Server,
	args []string,
) actions.Action {
	return &extensionAction{
		console:          console,
		extensionRunner:  extensionRunner,
		lazyEnv:          lazyEnv,
		extensionManager: extensionManager,
		azdServer:        azdServer,
		cmd:              cmd,
		args:             args,
	}
}

func (a *extensionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	extensionId, has := a.cmd.Annotations["extension.id"]
	if !has {
		return nil, fmt.Errorf("extension id not found")
	}

	extension, err := a.extensionManager.GetInstalled(extensions.FilterOptions{
		Id: extensionId,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get extension %s: %w", extensionId, err)
	}

	tracing.SetUsageAttributes(
		fields.ExtensionId.String(extension.Id),
		fields.ExtensionVersion.String(extension.Version))

	allEnv := []string{}
	allEnv = append(allEnv, os.Environ()...)

	forceColor := !color.NoColor
	if forceColor {
		allEnv = append(allEnv, "FORCE_COLOR=1")
	}

	// Pass the console width down to the child process
	// COLUMNS is a semi-standard environment variable used by many Unix programs to determine the width of the terminal.
	width := ux.ConsoleWidth()
	if width > 0 {
		allEnv = append(allEnv, fmt.Sprintf("COLUMNS=%d", width))
	}

	env, err := a.lazyEnv.GetValue()
	if err == nil && env != nil {
		allEnv = append(allEnv, env.Environ()...)
	}

	serverInfo, err := a.azdServer.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start gRPC server: %w", err)
	}

	defer a.azdServer.Stop()

	jwtToken, err := grpcserver.GenerateExtensionToken(extension, serverInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to generate extension token")
	}

	allEnv = append(allEnv,
		fmt.Sprintf("AZD_SERVER=%s", serverInfo.Address),
		fmt.Sprintf("AZD_ACCESS_TOKEN=%s", jwtToken),
	)

	// Propagate trace context to the extension process
	if traceEnv := tracing.Environ(ctx); len(traceEnv) > 0 {
		allEnv = append(allEnv, traceEnv...)
	}

	options := &extensions.InvokeOptions{
		Args: a.args,
		Env:  allEnv,
		// cmd extensions are always interactive (connected to terminal)
		Interactive: true,
	}

	_, err = a.extensionRunner.Invoke(ctx, extension, options)
	if err != nil {
		return nil, err
	}

	return nil, nil
}
