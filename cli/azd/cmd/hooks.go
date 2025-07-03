// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
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

const noHookFoundMessage = " (No hook found)"

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

	if err := hra.processHooks(
		ctx,
		hra.projectConfig.Path,
		hookName,
		fmt.Sprintf("Running %d %s command hook(s) for project", len(projectHooks), hookName),
		fmt.Sprintf("Project: %s Hook Output", hookName),
		projectHooks,
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

		if err := hra.processHooks(
			ctx,
			service.RelativePath,
			hookName,
			fmt.Sprintf("Running %d %s service hook(s) for %s", len(serviceHooks), hookName, service.Name),
			fmt.Sprintf("%s: %s hook output", service.Name, hookName),
			serviceHooks,
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
	spinnerMessage string,
	previewMessage string,
	hooks []*ext.HookConfig,
	skip bool,
) error {
	hra.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	if skip {
		hra.console.StopSpinner(ctx, spinnerMessage, input.StepSkipped)
		return nil
	}

	if len(hooks) == 0 {
		hra.console.StopSpinner(ctx, spinnerMessage+noHookFoundMessage, input.StepWarning)
		return nil
	}

	hookType, commandName := ext.InferHookType(hookName)

	for _, hook := range hooks {
		if err := hra.prepareHook(hookName, hook); err != nil {
			return err
		}

		err := hra.execHook(ctx, previewMessage, cwd, hookType, commandName, hook)
		if err != nil {
			hra.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
			return fmt.Errorf("failed running hook %s, %w", hookName, err)
		}

		// The previewer cancels the previous spinner so we need to restart/show it again.
		hra.console.StopSpinner(ctx, spinnerMessage, input.StepDone)
	}

	return nil
}

func (hra *hooksRunAction) execHook(
	ctx context.Context,
	previewMessage string,
	cwd string,
	hookType ext.HookType,
	commandName string,
	hook *ext.HookConfig,
) error {
	hookName := string(hookType) + commandName

	hooksMap := map[string][]*ext.HookConfig{
		hookName: {hook},
	}

	hooksManager := ext.NewHooksManager(cwd)
	hooksRunner := ext.NewHooksRunner(
		hooksManager, hra.commandRunner, hra.envManager, hra.console, cwd, hooksMap, hra.env, hra.serviceLocator)

	// For interactive hooks, let the hooks runner handle the output directly
	// For non-interactive hooks, use the previewer
	var runOptions *tools.ExecOptions
	if ext.GetBoolValue(hook.Interactive, false) {
		// Interactive hooks should use the real TTY, not the previewer
		runOptions = &tools.ExecOptions{}
	} else {
		// Non-interactive hooks use the previewer
		previewer := hra.console.ShowPreviewer(ctx, &input.ShowPreviewerOptions{
			Prefix:       "  ",
			Title:        previewMessage,
			MaxLineCount: 8,
		})
		defer hra.console.StopPreviewer(ctx, false)
		runOptions = &tools.ExecOptions{StdOut: previewer}
	}

	err := hooksRunner.RunHooks(ctx, hookType, runOptions, commandName)
	if err != nil {
		return err
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
