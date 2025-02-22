// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// bindExtension binds the extension to the root command
func bindExtension(
	serviceLocator ioc.ServiceLocator,
	root *actions.ActionDescriptor,
	extension *extensions.Extension,
) error {
	cmd := &cobra.Command{
		Use:                extension.Namespace,
		Short:              extension.Description,
		Long:               extension.Description,
		DisableFlagParsing: true,
	}

	cmd.SetHelpFunc(func(c *cobra.Command, s []string) {
		_ = serviceLocator.Invoke(invokeExtensionHelp)
	})

	root.Add(extension.Namespace, &actions.ActionDescriptorOptions{
		Command:        cmd,
		ActionResolver: newExtensionAction,
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupExtensions,
		},
	})

	return nil
}

// invokeExtensionHelp invokes the help for the extension
func invokeExtensionHelp(console input.Console, commandRunner exec.CommandRunner, extensionManager *extensions.Manager) {
	extensionNamespace := os.Args[1]
	extension, err := extensionManager.GetInstalled(extensions.GetInstalledOptions{
		Namespace: extensionNamespace,
	})
	if err != nil {
		fmt.Println("Failed running help")
	}

	homeDir, err := config.GetUserConfigDir()
	if err != nil {
		fmt.Println("Failed running help")
	}

	extensionPath := filepath.Join(homeDir, extension.Path)

	runArgs := exec.
		NewRunArgs(extensionPath, os.Args[2:]...).
		WithStdIn(console.Handles().Stdin).
		WithStdOut(console.Handles().Stdout).
		WithStdErr(console.Handles().Stderr)

	_, err = commandRunner.Run(context.Background(), runArgs)
	if err != nil {
		fmt.Println("Failed running help")
	}
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
	extensionNamespace := a.cmd.Use

	extension, err := a.extensionManager.GetInstalled(extensions.GetInstalledOptions{
		Namespace: extensionNamespace,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get extension %s: %w", extensionNamespace, err)
	}

	allEnv := []string{}
	allEnv = append(allEnv, os.Environ()...)

	forceColor := !color.NoColor
	if forceColor {
		allEnv = append(allEnv, "FORCE_COLOR=1")
	}

	env, err := a.lazyEnv.GetValue()
	if err == nil && env != nil {
		allEnv = append(allEnv, env.Environ()...)
	}

	serverInfo, err := a.azdServer.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start gRPC server: %w", err)
	}

	jwtToken, err := grpcserver.GenerateExtensionToken(extension, serverInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to generate extension token")
	}

	allEnv = append(allEnv,
		fmt.Sprintf("AZD_SERVER=%s", serverInfo.Address),
		fmt.Sprintf("AZD_ACCESS_TOKEN=%s", jwtToken),
	)

	options := &extensions.InvokeOptions{
		Args:   a.args,
		Env:    allEnv,
		StdIn:  a.console.Handles().Stdin,
		StdOut: a.console.Handles().Stdout,
		StdErr: a.console.Handles().Stderr,
	}

	_, err = a.extensionRunner.Invoke(ctx, extension, options)
	if err != nil {
		log.Printf("Failed to invoke extension %s: %v\n", extensionNamespace, err)
	}

	if err = a.azdServer.Stop(); err != nil {
		log.Printf("Failed to stop gRPC server: %v\n", err)
	}

	return nil, nil
}
