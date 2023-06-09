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
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func hooksActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("hooks", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "hooks",
			Short: "Manage hooks.",
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupManage,
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
	envFlag
	global      *internal.GlobalCommandOptions
	platform    string
	interactive bool
	service     string
}

func (f *hooksRunFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.envFlag.Bind(local, global)
	f.global = global

	local.StringVar(&f.platform, "platform", "", "Forces hooks to run for the specified platform.")
	local.BoolVar(&f.interactive, "interactive", false, "Forces all hooks to run in interactive mode for testing")
	local.StringVar(&f.service, "service", "", "Only runs hooks for the specified service.")
}

type hooksRunAction struct {
	projectConfig *project.ProjectConfig
	env           *environment.Environment
	commandRunner exec.CommandRunner
	console       input.Console
	flags         *hooksRunFlags
	args          []string
}

func newHooksRunAction(
	projectConfig *project.ProjectConfig,
	env *environment.Environment,
	commandRunner exec.CommandRunner,
	console input.Console,
	flags *hooksRunFlags,
	args []string,
) actions.Action {
	return &hooksRunAction{
		projectConfig: projectConfig,
		env:           env,
		commandRunner: commandRunner,
		console:       console,
		flags:         flags,
		args:          args,
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
			output.WithHighLightFormat(hra.env.GetEnvName()),
		),
	})

	// Validate hook type
	if _, _, err := ext.InferHookType(hookName); err != nil {
		return nil, fmt.Errorf("hook name is invalid. Hook names should start with 'pre' or 'post', %w", err)
	}

	// Validate service name
	if _, ok := hra.projectConfig.Services[hra.flags.service]; hra.flags.service != "" && !ok {
		return nil, fmt.Errorf("service name '%s' doesn't exist", hra.flags.service)
	}

	// Project level hooks
	projectHooksMessage := "Running command hook for project"
	if err := hra.processHooks(
		ctx,
		hra.projectConfig.Path,
		hookName,
		projectHooksMessage,
		hra.projectConfig.Hooks,
		false,
	); err != nil {
		return nil, err
	}

	// Service level hooks
	for serviceName, service := range hra.projectConfig.Services {
		skip := hra.flags.service != "" && serviceName != hra.flags.service

		serviceHookMessage := fmt.Sprintf("Running service hook for %s", serviceName)
		if err := hra.processHooks(
			ctx,
			service.RelativePath,
			hookName,
			serviceHookMessage,
			service.Hooks,
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
	message string,
	hooks map[string]*ext.HookConfig,
	skip bool,
) error {
	hra.console.ShowSpinner(ctx, message, input.Step)

	if skip {
		hra.console.StopSpinner(ctx, message, input.StepSkipped)
		return nil
	}

	hook, ok := hooks[hookName]
	if !ok {
		hra.console.StopSpinner(ctx, message+noHookFoundMessage, input.StepWarning)
		return nil
	}

	hookType, commandName, err := ext.InferHookType(hookName)
	if err != nil {
		return err
	}

	if err := hra.prepareHook(hookName, hook); err != nil {
		return err
	}

	if hook.Interactive {
		hra.console.StopSpinner(ctx, "", input.StepDone)
		hra.console.Message(ctx, output.WithBold(output.WithUnderline(message)))
	}

	err = hra.execHook(ctx, cwd, hookType, commandName, hook)
	if err != nil {
		hra.console.StopSpinner(ctx, message, input.StepFailed)
		return fmt.Errorf("failed running hook %s, %w", hookName, err)
	}

	hra.console.StopSpinner(ctx, message, input.StepDone)
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

	hooks := map[string]*ext.HookConfig{
		hookName: hook,
	}

	hooksManager := ext.NewHooksManager(cwd)
	hooksRunner := ext.NewHooksRunner(hooksManager, hra.commandRunner, hra.console, cwd, hooks, hra.env)
	err := hooksRunner.RunHooks(ctx, hookType, commandName)
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

	// Don't display the 'Executing hook...' messages
	hook.Quiet = true
	if hra.flags.interactive {
		hook.Interactive = true
	}

	hra.configureHookFlags(hook.Windows)
	hra.configureHookFlags(hook.Posix)

	return nil
}

func (hra *hooksRunAction) configureHookFlags(hook *ext.HookConfig) {
	if hook == nil {
		return
	}

	hook.Quiet = true
	if hra.flags.interactive {
		hook.Interactive = true
	}
}
