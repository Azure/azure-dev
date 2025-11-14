// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cmd/actions"
	"github.com/azure/azure-dev/internal"
	"github.com/azure/azure-dev/pkg/environment"
	"github.com/azure/azure-dev/pkg/exec"
	"github.com/azure/azure-dev/pkg/ext"
	"github.com/azure/azure-dev/pkg/input"
	"github.com/azure/azure-dev/pkg/ioc"
	"github.com/azure/azure-dev/pkg/output"
	"github.com/azure/azure-dev/pkg/output/ux"
	"github.com/azure/azure-dev/pkg/project"
	"github.com/azure/azure-dev/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func hooksActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("hooks", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "hooks",
			Short: "Develop, test and run hooks for a project.",
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupBeta,
		},
	})

	group.Add("run", &actions.ActionDescriptorOptions{
		Command:        newHooksRunCmd(),
		FlagsResolver:  newHooksRunFlags,
		ActionResolver: newHooksRunAction,
	})

	return group
}

func newHooksRunFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *hooksRunFlags {
	flags := &hooksRunFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newHooksRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <name>",
		Short: "Runs the specified hook for the project and services",
		Args:  cobra.ExactArgs(1),
	}
}

type hooksRunFlags struct {
	internal.EnvFlag
	global   *internal.GlobalCommandOptions
	platform string
	service  string
}

func (f *hooksRunFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global

	local.StringVar(&f.platform, "platform", "", "Forces hooks to run for the specified platform.")
	local.StringVar(&f.service, "service", "", "Only runs hooks for the specified service.")
}

type hooksRunAction struct {
	projectConfig  *project.ProjectConfig
	env            *environment.Environment
	envManager     environment.Manager
	importManager  *project.ImportManager
	commandRunner  exec.CommandRunner
	console        input.Console
	flags          *hooksRunFlags
	args           []string
	serviceLocator ioc.ServiceLocator
}

func newHooksRunAction(
	projectConfig *project.ProjectConfig,
	importManager *project.ImportManager,
	env *environment.Environment,
	envManager environment.Manager,
	commandRunner exec.CommandRunner,
	console input.Console,
	flags *hooksRunFlags,
	args []string,
	serviceLocator ioc.ServiceLocator,
) actions.Action {
	return &hooksRunAction{
		projectConfig:  projectConfig,
		env:            env,
		envManager:     envManager,
		commandRunner:  commandRunner,
		console:        console,
		flags:          flags,
		args:           args,
		importManager:  importManager,
		serviceLocator: serviceLocator,
	}
}

type hookContextType string

const (
	hookContextProject hookContextType = "command"
	hookContextService hookContextType = "service"
)

func (hra *hooksRunAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	hookName := hra.args[0]

	// Command title
	hra.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Running hooks (azd hooks run)",
		TitleNote: fmt.Sprintf(
			"Finding and executing %s hooks for environment %s",
			output.WithHighLightFormat(hookName),
			output.WithHighLightFormat(hra.env.Name()),
		),
	})

	// Validate hooks and display warnings
	if err := hra.validateAndWarnHooks(ctx); err != nil {
		return nil, fmt.Errorf("failed validating hooks: %w", err)
	}

	// Validate service name
	if hra.flags.service != "" {
		if has, err := hra.importManager.HasService(ctx, hra.projectConfig, hra.flags.service); err != nil {
			return nil, err
		} else if !has {
			return nil, fmt.Errorf("service name '%s' doesn't exist", hra.flags.service)
		}
	}

	// Project level hooks
	projectHooks := hra.projectConfig.Hooks[hookName]

	hra.console.Message(ctx, output.WithHighLightFormat("Project"))
	if err := hra.processHooks(
		ctx,
		hra.projectConfig.Path,
		hookName,
		projectHooks,
		hookContextProject,
		false,
	); err != nil {
		return nil, err
	}

	stableServices, err := hra.importManager.ServiceStable(ctx, hra.projectConfig)
	if err != nil {
		return nil, err
	}

	// Service level hooks
	for _, service := range stableServices {
		serviceHooks := service.Hooks[hookName]
		skip := hra.flags.service != "" && service.Name != hra.flags.service

		hra.console.Message(ctx, "\n"+output.WithHighLightFormat(service.Name))
		if err := hra.processHooks(
			ctx,
			service.RelativePath,
			hookName,
			serviceHooks,
			hookContextService,
			skip,
		); err != nil {
			return nil, err
		}
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Your hooks have been run successfully",
		},
	}, nil
}

func (hra *hooksRunAction) processHooks(
	ctx context.Context,
	cwd string,
	hookName string,
	hooks []*ext.HookConfig,
	contextType hookContextType,
	skip bool,
) error {
	if len(hooks) == 0 {
		hra.console.MessageUxItem(ctx, &ux.WarningAltMessage{Message: "No hooks found"})
		return nil
	}

	if skip {
		// When skipping, show individual skip messages for each hook that would have run
		for i := range hooks {
			hra.console.MessageUxItem(ctx, &ux.SkippedMessage{
				Message: fmt.Sprintf("service hook %d/%d", i+1, len(hooks)),
			})
		}

		return nil
	}

	hookType, commandName := ext.InferHookType(hookName)

	for idx, hook := range hooks {
		if err := hra.prepareHook(hookName, hook); err != nil {
			return err
		}

		hra.console.Message(ctx, output.WithBold("%s hook %d/%d:", contextType, idx+1, len(hooks)))

		err := hra.execHook(ctx, cwd, hookType, commandName, hook)
		if err != nil {
			return fmt.Errorf("failed running hook %s, %w", hookName, err)
		}

		hra.console.MessageUxItem(ctx, &ux.DoneMessage{
			Message: "Successfully executed hook",
		})

		// Add blank line after each hook except the last one
		if idx < len(hooks)-1 {
			hra.console.Message(ctx, "")
		}
	}

	return nil
}

func (hra *hooksRunAction) execHook(
	ctx context.Context,
	cwd string,
	hookType ext.HookType,
	commandName string,
	hook *ext.HookConfig,
) error {
	hookName := string(hookType) + commandName

	hooksMap := map[string][]*ext.HookConfig{
		hookName: {hook},
	}

	hooksManager := ext.NewHooksManager(cwd, hra.commandRunner)
	hooksRunner := ext.NewHooksRunner(
		hooksManager, hra.commandRunner, hra.envManager, hra.console, cwd, hooksMap, hra.env, hra.serviceLocator)

	// Always run in interactive mode for 'azd hooks run', to help with testing/debugging
	runOptions := &tools.ExecOptions{
		Interactive: to.Ptr(true),
	}

	err := hooksRunner.RunHooks(ctx, hookType, runOptions, commandName)
	if err != nil {
		return err
	}

	return nil
}

// Validates hooks and displays warnings for default shell usage and other issues
func (hra *hooksRunAction) validateAndWarnHooks(ctx context.Context) error {
	// Collect all hooks from project and services
	allHooks := make(map[string][]*ext.HookConfig)

	// Add project hooks
	for hookName, hookConfigs := range hra.projectConfig.Hooks {
		allHooks[hookName] = append(allHooks[hookName], hookConfigs...)
	}

	// Add service hooks
	stableServices, err := hra.importManager.ServiceStable(ctx, hra.projectConfig)
	if err == nil {
		for _, service := range stableServices {
			for hookName, hookConfigs := range service.Hooks {
				allHooks[hookName] = append(allHooks[hookName], hookConfigs...)
			}
		}
	}

	// Create hooks manager and validate
	hooksManager := ext.NewHooksManager(hra.projectConfig.Path, hra.commandRunner)
	validationResult := hooksManager.ValidateHooks(ctx, allHooks)

	// Display any warnings
	for _, warning := range validationResult.Warnings {
		hra.console.MessageUxItem(ctx, &ux.WarningMessage{
			Description: warning.Message,
		})
		if warning.Suggestion != "" {
			hra.console.Message(ctx, warning.Suggestion)
		}
		hra.console.Message(ctx, "")
	}

	return nil
}

// Overrides the configured hooks from command line flags
func (hra *hooksRunAction) prepareHook(name string, hook *ext.HookConfig) error {
	// Enable testing cross platform
	if hra.flags.platform != "" {
		platformType := ext.HookPlatformType(hra.flags.platform)
		switch platformType {
		case ext.HookPlatformWindows:
			if hook.Windows == nil {
				return fmt.Errorf("hook is not configured for Windows")
			} else {
				*hook = *hook.Windows
			}
		case ext.HookPlatformPosix:
			if hook.Posix == nil {
				return fmt.Errorf("hook is not configured for Posix")
			} else {
				*hook = *hook.Posix
			}
		default:
			return fmt.Errorf("platform %s is not valid. Supported values are windows & posix", hra.flags.platform)
		}
	}

	hook.Name = name

	return nil
}
