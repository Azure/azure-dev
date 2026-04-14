// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
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
		Short: "Runs the specified hook for the project, provisioning layers, and services",
		Args:  cobra.ExactArgs(1),
	}
}

type hooksRunFlags struct {
	internal.EnvFlag
	global   *internal.GlobalCommandOptions
	layer    string
	platform string
	service  string
}

func (f *hooksRunFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global

	local.StringVar(&f.layer, "layer", "", "Only runs hooks for the specified provisioning layer.")
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
	hookContextProject hookContextType = "project"
	hookContextLayer   hookContextType = "layer"
	hookContextService hookContextType = "service"
)

// knownHookNames is the set of built-in azd hook names.
// Extension-defined hooks are not included here; they are hashed in telemetry.
// See https://github.com/Azure/azure-dev/issues/7348 for tracking.
var knownHookNames = map[string]bool{
	"prebuild":      true,
	"postbuild":     true,
	"predeploy":     true,
	"postdeploy":    true,
	"predown":       true,
	"postdown":      true,
	"prepackage":    true,
	"postpackage":   true,
	"preprovision":  true,
	"postprovision": true,
	"prepublish":    true,
	"postpublish":   true,
	"prerestore":    true,
	"postrestore":   true,
	"preup":         true,
	"postup":        true,
}

func (hra *hooksRunAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	hookName := hra.args[0]

	if hra.flags.service != "" && hra.flags.layer != "" {
		return nil,

			&internal.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"--service and --layer cannot be used together: %w", internal.ErrInvalidFlagCombination),
				Suggestion: "Choose either '--service' to run service hooks or '--layer' to run provisioning layer hooks.",
			}
	}

	hookType := "project"
	if hra.flags.layer != "" {
		hookType = "layer"
	} else if hra.flags.service != "" {
		hookType = "service"
	}

	// Log known hook names raw; hash unknown names to avoid logging arbitrary user input.
	hookNameAttr := fields.StringHashed(fields.HooksNameKey, hookName)
	if knownHookNames[hookName] {
		hookNameAttr = fields.HooksNameKey.String(hookName)
	}
	tracing.SetUsageAttributes(
		hookNameAttr,
		fields.HooksTypeKey.String(hookType),
	)

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
			return nil, &internal.ErrorWithSuggestion{
				Err:        fmt.Errorf("service '%s': %w", hra.flags.service, internal.ErrServiceNotFound),
				Suggestion: "Check the service name in azure.yaml or run 'azd show' to list services.",
			}
		}
	}

	if hra.flags.layer != "" {
		if _, err := hra.projectConfig.Infra.GetLayer(hra.flags.layer); err != nil {
			return nil, err
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

	for _, layer := range hra.projectConfig.Infra.Layers {
		layerPath := layer.AbsolutePath(hra.projectConfig.Path)

		skip := hra.flags.layer != "" && layer.Name != hra.flags.layer

		hra.console.Message(ctx, "\n"+output.WithHighLightFormat(fmt.Sprintf("Layer: %s", layer.Name)))
		if err := hra.processHooks(
			ctx,
			layerPath,
			hookName,
			layer.Hooks[hookName],
			hookContextLayer,
			skip,
		); err != nil {
			return nil, err
		}
	}

	// Service level hooks
	for _, service := range stableServices {
		serviceHooks := service.Hooks[hookName]
		skip := hra.flags.service != "" && service.Name != hra.flags.service

		hra.console.Message(ctx, "\n"+output.WithHighLightFormat(service.Name))
		if err := hra.processHooks(
			ctx,
			service.Path(),
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
				Message: fmt.Sprintf("%s hook %d/%d", contextType, i+1, len(hooks)),
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

		err := hra.execHook(ctx, cwd, hookType, commandName, hook, contextType)
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
	ht ext.HookType,
	commandName string,
	hook *ext.HookConfig,
	contextType hookContextType,
) error {
	hookName := string(ht) + commandName

	hooksMap := map[string][]*ext.HookConfig{
		hookName: {hook},
	}

	hooksManager := ext.NewHooksManager(ext.HooksManagerOptions{
		Cwd: cwd, ProjectDir: hra.projectConfig.Path,
	}, hra.commandRunner)
	hooksRunner := ext.NewHooksRunner(
		hooksManager, hra.commandRunner, hra.envManager, hra.console, cwd, hooksMap, hra.env, hra.serviceLocator)

	hookType := "project"
	switch contextType {
	case hookContextLayer:
		hookType = "layer"
	case hookContextService:
		hookType = "service"
	}

	// Always run in interactive mode for 'azd hooks run', to help with testing/debugging
	runOptions := &tools.ExecutionContext{
		Interactive: new(true),
	}

	err := hooksRunner.RunHooks(ctx, ht, hookType, runOptions, commandName)
	if err != nil {
		return err
	}

	return nil
}

// Validates hooks and displays warnings for default shell usage and other issues
func (hra *hooksRunAction) validateAndWarnHooks(ctx context.Context) error {
	warningKeys := map[string]struct{}{}
	validateAndWarn := func(cwd string, hooks map[string][]*ext.HookConfig) {
		if len(hooks) == 0 {
			return
		}

		hooksManager := ext.NewHooksManager(ext.HooksManagerOptions{
			Cwd: cwd, ProjectDir: hra.projectConfig.Path,
		}, hra.commandRunner)
		validationResult := hooksManager.ValidateHooks(ctx, hooks)

		for _, warning := range validationResult.Warnings {
			key := warning.Message + "\x00" + warning.Suggestion
			if _, has := warningKeys[key]; has {
				continue
			}

			warningKeys[key] = struct{}{}
			hra.console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: warning.Message,
			})
			if warning.Suggestion != "" {
				hra.console.Message(ctx, warning.Suggestion)
			}
			hra.console.Message(ctx, "")
		}
	}

	validateAndWarn(hra.projectConfig.Path, hra.projectConfig.Hooks)

	stableServices, err := hra.importManager.ServiceStable(ctx, hra.projectConfig)
	if err == nil {
		for _, service := range stableServices {
			validateAndWarn(service.Path(), service.Hooks)
		}
	}

	for _, layer := range hra.projectConfig.Infra.Layers {
		validateAndWarn(layer.AbsolutePath(hra.projectConfig.Path), layer.Hooks)
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
