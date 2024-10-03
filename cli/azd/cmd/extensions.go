package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/spf13/cobra"
)

// bindExtensions binds the extensions to the root command
func bindExtensions(
	serviceLocator ioc.ServiceLocator,
	root *actions.ActionDescriptor,
	extensions map[string]*extensions.Extension,
) error {
	for key, extension := range extensions {
		if extension.Name == "" {
			extension.Name = key
		}

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
		Use:                extension.Name,
		Short:              extension.Description,
		Long:               extension.Description,
		DisableFlagParsing: true,
	}

	cmd.SetHelpFunc(func(c *cobra.Command, s []string) {
		serviceLocator.Invoke(invokeExtensionHelp)
	})

	root.Add(extension.Name, &actions.ActionDescriptorOptions{
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
	extensionName := os.Args[1]
	extension, err := extensionManager.Get(extensionName)
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
}

func newExtensionAction(
	console input.Console,
	commandRunner exec.CommandRunner,
	lazyEnv *lazy.Lazy[*environment.Environment],
	extensionManager *extensions.Manager,
) actions.Action {
	return &extensionAction{
		console:          console,
		commandRunner:    commandRunner,
		lazyEnv:          lazyEnv,
		extensionManager: extensionManager,
	}
}

func (a *extensionAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	extensionName := os.Args[1]

	extension, err := a.extensionManager.Get(extensionName)
	if err != nil {
		return nil, fmt.Errorf("failed to get extension %s: %w", extensionName, err)
	}

	allEnv := []string{}
	allEnv = append(allEnv, os.Environ()...)

	env, err := a.lazyEnv.GetValue()
	if err == nil && env != nil {
		allEnv = append(allEnv, env.Environ()...)
	}

	allArgs := []string{}
	allArgs = append(allArgs, os.Args[2:]...)

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
		NewRunArgs(extensionPath, allArgs...).
		WithCwd(cwd).
		WithEnv(allEnv).
		WithStdIn(a.console.Handles().Stdin).
		WithStdOut(a.console.Handles().Stdout).
		WithStdErr(a.console.Handles().Stderr)

	_, err = a.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return nil, err
	}

	return nil, nil
}
