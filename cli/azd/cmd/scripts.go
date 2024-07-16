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
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func scriptsActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("script", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "script",
			Short: fmt.Sprintf("Develop, test and run scripts for an application. %s", output.WithWarningFormat("(Beta)")),
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupConfig,
		},
	})

	group.Add("run", &actions.ActionDescriptorOptions{
		Command:        newScriptsRunCmd(),
		FlagsResolver:  newScriptsRunFlags,
		ActionResolver: newScriptsRunAction,
	})

	return group
}

func newScriptsRunFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *scriptsRunFlags {
	flags := &scriptsRunFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newScriptsRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <name>",
		Short: "Runs the specified script for the project",
		Args:  cobra.ExactArgs(1),
	}
}

type scriptsRunFlags struct {
	internal.EnvFlag
	global   *internal.GlobalCommandOptions
	platform string
}

func (f *scriptsRunFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global

	local.StringVar(&f.platform, "platform", "", "Forces scripts to run for the specified platform.")
}

type scriptsRunAction struct {
	projectConfig *project.ProjectConfig
	env           *environment.Environment
	envManager    environment.Manager
	commandRunner exec.CommandRunner
	console       input.Console
	flags         *scriptsRunFlags
	args          []string
}

func newScriptsRunAction(
	projectConfig *project.ProjectConfig,
	env *environment.Environment,
	envManager environment.Manager,
	commandRunner exec.CommandRunner,
	console input.Console,
	flags *scriptsRunFlags,
	args []string,
) actions.Action {
	return &scriptsRunAction{
		projectConfig: projectConfig,
		env:           env,
		envManager:    envManager,
		commandRunner: commandRunner,
		console:       console,
		flags:         flags,
		args:          args,
	}
}

const noScriptFoundMessage = " (No script found)"

func (sra *scriptsRunAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	scriptName := sra.args[0]

	// Command title
	sra.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Running scripts (azd scripts run)",
		TitleNote: fmt.Sprintf(
			"Finding and executing %s scripts for environment %s",
			output.WithHighLightFormat(scriptName),
			output.WithHighLightFormat(sra.env.Name()),
		),
	})

	// Project level scripts
	if err := sra.processScripts(
		ctx,
		sra.projectConfig.Path,
		scriptName,
		fmt.Sprintf("Running %s command script for project", scriptName),
		fmt.Sprintf("Project: %s Script Output", scriptName),
		sra.projectConfig.Scripts, // Use projectConfig.Scripts directly
	); err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: "Your scripts have been run successfully",
		},
	}, nil
}

func (sra *scriptsRunAction) processScripts(
	ctx context.Context,
	cwd string,
	scriptName string,
	spinnerMessage string,
	previewMessage string,
	scripts map[string]*ext.HookConfig,
) error {
	sra.console.ShowSpinner(ctx, spinnerMessage, input.Step)

	script, ok := scripts[scriptName]
	if !ok {
		sra.console.StopSpinner(ctx, spinnerMessage+noScriptFoundMessage, input.StepWarning)
		return nil
	}

	if err := sra.prepareScript(scriptName, script); err != nil {
		return err
	}

	err := sra.execScript(ctx, previewMessage, cwd, script)
	if err != nil {
		sra.console.StopSpinner(ctx, spinnerMessage, input.StepFailed)
		return fmt.Errorf("failed running script %s, %w", scriptName, err)
	}

	sra.console.StopSpinner(ctx, spinnerMessage, input.StepDone)

	return nil
}

func (sra *scriptsRunAction) execScript(
	ctx context.Context,
	previewMessage string,
	cwd string,
	script *ext.HookConfig,
) error {
	scripts := map[string]*ext.HookConfig{
		script.Name: script,
	}

	scriptsManager := ext.NewHooksManager(cwd)
	scriptsRunner := ext.NewHooksRunner(scriptsManager, sra.commandRunner, sra.envManager, sra.console, cwd, scripts, sra.env)

	previewer := sra.console.ShowPreviewer(ctx, &input.ShowPreviewerOptions{
		Prefix:       "  ",
		Title:        previewMessage,
		MaxLineCount: 8,
	})
	defer sra.console.StopPreviewer(ctx, false)

	runOptions := &tools.ExecOptions{StdOut: previewer}
	err := scriptsRunner.RunHooks(ctx, ext.HookTypeNone, runOptions, script.Name)
	if err != nil {
		return err
	}

	return nil
}

func (sra *scriptsRunAction) prepareScript(name string, script *ext.HookConfig) error {
	if sra.flags.platform != "" {
		platformType := ext.HookPlatformType(sra.flags.platform)
		switch platformType {
		case ext.HookPlatformWindows:
			if script.Windows == nil {
				return fmt.Errorf("script is not configured for Windows")
			} else {
				*script = *script.Windows
			}
		case ext.HookPlatformPosix:
			if script.Posix == nil {
				return fmt.Errorf("script is not configured for Posix")
			} else {
				*script = *script.Posix
			}
		default:
			return fmt.Errorf("platform %s is not valid. Supported values are windows & posix", sra.flags.platform)
		}
	}

	script.Name = name
	script.Interactive = true

	sra.configureScriptFlags(script.Windows)
	sra.configureScriptFlags(script.Posix)

	return nil
}

func (sra *scriptsRunAction) configureScriptFlags(script *ext.HookConfig) {
	if script == nil {
		return
	}

	script.Interactive = true
}
