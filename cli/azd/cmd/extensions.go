package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// bindExtensions binds the extensions to the root command
func bindExtensions(
	serviceLocator ioc.ServiceLocator,
	root *actions.ActionDescriptor,
	extensions map[string]*extensions.Extension,
) error {
	for _, extension := range extensions {
		if err := bindExtension(serviceLocator, root, extension); err != nil {
			return err
		}
	}

	return nil
}

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

	homeDir, err := os.UserHomeDir()
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
	commandRunner    exec.CommandRunner
	lazyEnv          *lazy.Lazy[*environment.Environment]
	extensionManager *extensions.Manager
	cmd              *cobra.Command
	args             []string
}

func newExtensionAction(
	console input.Console,
	commandRunner exec.CommandRunner,
	lazyEnv *lazy.Lazy[*environment.Environment],
	extensionManager *extensions.Manager,
	cmd *cobra.Command,
	args []string,
) actions.Action {
	return &extensionAction{
		console:          console,
		commandRunner:    commandRunner,
		lazyEnv:          lazyEnv,
		extensionManager: extensionManager,
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

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get user home directory: %w", err)
	}

	extensionPath := filepath.Join(homeDir, extension.Path)

	_, err = os.Stat(extensionPath)
	if err != nil {
		return nil, fmt.Errorf("extension path was not found: %s: %w", extensionPath, err)
	}

	runArgs := exec.
		NewRunArgs(extensionPath, a.args...).
		WithCwd(cwd).
		WithEnv(allEnv).
		WithStdIn(a.console.Handles().Stdin).
		WithStdOut(a.console.Handles().Stdout).
		WithStdErr(a.console.Handles().Stderr)

	_, err = a.commandRunner.Run(ctx, runArgs)
	if err != nil {
		log.Printf("Failed to run extension %s: %v\n", extensionNamespace, err)
	}

	return nil, nil
}
