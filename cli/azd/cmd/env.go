// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func envActions(root *actions.ActionDescriptor) *actions.ActionDescriptor {
	group := root.Add("env", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{
			Use:   "env",
			Short: "Manage environments.",
		},
		HelpOptions: actions.ActionHelpOptions{
			Description: getCmdEnvHelpDescription,
		},
		GroupingOptions: actions.CommandGroupOptions{
			RootLevelHelp: actions.CmdGroupManage,
		},
	})

	group.Add("set", &actions.ActionDescriptorOptions{
		Command:        newEnvSetCmd(),
		FlagsResolver:  newEnvSetFlags,
		ActionResolver: newEnvSetAction,
	})

	group.Add("select", &actions.ActionDescriptorOptions{
		Command:        newEnvSelectCmd(),
		ActionResolver: newEnvSelectAction,
	})

	group.Add("new", &actions.ActionDescriptorOptions{
		Command:        newEnvNewCmd(),
		FlagsResolver:  newEnvNewFlags,
		ActionResolver: newEnvNewAction,
	})

	group.Add("list", &actions.ActionDescriptorOptions{
		Command:        newEnvListCmd(),
		ActionResolver: newEnvListAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.TableFormat},
		DefaultFormat:  output.TableFormat,
	})

	group.Add("refresh", &actions.ActionDescriptorOptions{
		Command:        newEnvRefreshCmd(),
		FlagsResolver:  newEnvRefreshFlags,
		ActionResolver: newEnvRefreshAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.NoneFormat},
		DefaultFormat:  output.NoneFormat,
	})

	group.Add("get-values", &actions.ActionDescriptorOptions{
		Command:        newEnvGetValuesCmd(),
		FlagsResolver:  newEnvGetValuesFlags,
		ActionResolver: newEnvGetValuesAction,
		OutputFormats:  []output.Format{output.JsonFormat, output.EnvVarsFormat},
		DefaultFormat:  output.EnvVarsFormat,
	})

	return group
}

func newEnvSetFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envSetFlags {
	flags := &envSetFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newEnvSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Manage your environment settings.",
		Args:  cobra.ExactArgs(2),
	}
}

type envSetFlags struct {
	envFlag
	global *internal.GlobalCommandOptions
}

func (f *envSetFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.envFlag.Bind(local, global)
	f.global = global
}

type envSetAction struct {
	console input.Console
	azdCtx  *azdcontext.AzdContext
	env     *environment.Environment
	flags   *envSetFlags
	args    []string
}

func newEnvSetAction(
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	console input.Console,
	flags *envSetFlags,
	args []string,
) actions.Action {
	return &envSetAction{
		console: console,
		azdCtx:  azdCtx,
		env:     env,
		flags:   flags,
		args:    args,
	}
}

func (e *envSetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	e.env.DotenvSet(e.args[0], e.args[1])

	if err := e.env.Save(); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return nil, nil
}

func newEnvSelectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "select <environment>",
		Short: "Set the default environment.",
		Args:  cobra.ExactArgs(1),
	}
}

type envSelectAction struct {
	azdCtx *azdcontext.AzdContext
	args   []string
}

func newEnvSelectAction(azdCtx *azdcontext.AzdContext, args []string) actions.Action {
	return &envSelectAction{
		azdCtx: azdCtx,
		args:   args,
	}
}

func (e *envSelectAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if err := e.azdCtx.SetDefaultEnvironmentName(e.args[0]); err != nil {
		return nil, fmt.Errorf("setting default environment: %w", err)
	}

	return nil, nil
}

func newEnvListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List environments.",
		Aliases: []string{"ls"},
	}
}

type envListAction struct {
	azdCtx    *azdcontext.AzdContext
	formatter output.Formatter
	writer    io.Writer
}

func newEnvListAction(azdCtx *azdcontext.AzdContext, formatter output.Formatter, writer io.Writer) actions.Action {
	return &envListAction{
		azdCtx:    azdCtx,
		formatter: formatter,
		writer:    writer,
	}
}

func (e *envListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	envs, err := e.azdCtx.ListEnvironments()

	if err != nil {
		return nil, fmt.Errorf("listing environments: %w", err)
	}

	if e.formatter.Kind() == output.TableFormat {
		columns := []output.Column{
			{
				Heading:       "NAME",
				ValueTemplate: "{{.Name}}",
			},
			{
				Heading:       "DEFAULT",
				ValueTemplate: "{{.IsDefault}}",
			},
		}

		err = e.formatter.Format(envs, e.writer, output.TableFormatterOptions{
			Columns: columns,
		})
	} else {
		err = e.formatter.Format(envs, e.writer, nil)
	}
	if err != nil {
		return nil, err
	}

	return nil, nil
}

type envNewFlags struct {
	subscription string
	location     string
	global       *internal.GlobalCommandOptions
}

func (f *envNewFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVar(
		&f.subscription,
		"subscription",
		"",
		"Name or ID of an Azure subscription to use for the new environment",
	)
	local.StringVarP(&f.location, "location", "l", "", "Azure location for the new environment")

	f.global = global
}

func newEnvNewFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envNewFlags {
	flags := &envNewFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newEnvNewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new <environment>",
		Short: "Create a new environment.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
}

type envNewAction struct {
	azdCtx  *azdcontext.AzdContext
	flags   *envNewFlags
	args    []string
	console input.Console
}

func newEnvNewAction(
	azdCtx *azdcontext.AzdContext,
	flags *envNewFlags,
	args []string,
	console input.Console,
) actions.Action {
	return &envNewAction{
		azdCtx:  azdCtx,
		flags:   flags,
		args:    args,
		console: console,
	}
}

func (en *envNewAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	environmentName := ""
	if len(en.args) >= 1 {
		environmentName = en.args[0]
	}

	envSpec := environmentSpec{
		environmentName: environmentName,
		subscription:    en.flags.subscription,
		location:        en.flags.location,
	}

	env, err := createEnvironment(ctx, envSpec, en.azdCtx, en.console)
	if err != nil {
		return nil, fmt.Errorf("creating new environment: %w", err)
	}

	if err := en.azdCtx.SetDefaultEnvironmentName(env.GetEnvName()); err != nil {
		return nil, fmt.Errorf("saving default environment: %w", err)
	}

	return nil, nil
}

type envRefreshFlags struct {
	global *internal.GlobalCommandOptions
	envFlag
}

func (er *envRefreshFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	er.envFlag.Bind(local, global)
	er.global = global
}

func newEnvRefreshFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envRefreshFlags {
	flags := &envRefreshFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newEnvRefreshCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "refresh <environment>",
		Short: "Refresh environment settings by using information from a previous infrastructure provision.",

		// We want to support the usual -e / --environment arguments as all our commands which take environments do, but for
		// ergonomics, we'd also like you to be able to run `azd env refresh some-environment-name` to behave the same way as
		// `azd env refresh -e some-environment-name` would have.
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MaximumNArgs(1)(cmd, args); err != nil {
				return err
			}

			if len(args) == 0 {
				return nil
			}

			if flagValue, err := cmd.Flags().GetString(environmentNameFlag); err == nil {
				if flagValue != "" && args[0] != flagValue {
					return errors.New(
						"the --environment flag and an explicit environment name as an argument may not be used together")
				}
			}

			return cmd.Flags().Set(environmentNameFlag, args[0])
		},
		Annotations: map[string]string{},
	}

	// This is like the Use property above, but does not include the hint to show an environment name is supported. This
	// is used by some tests which need to construct a valid command line to run `azd` and here using `<environment>` would
	// be invalid, since it is an invalid name.
	cmd.Annotations["azdtest.use"] = "refresh"
	return cmd
}

type envRefreshAction struct {
	provisionManager *provisioning.Manager
	projectConfig    *project.ProjectConfig
	projectManager   project.ProjectManager
	env              *environment.Environment
	flags            *envRefreshFlags
	console          input.Console
	formatter        output.Formatter
	writer           io.Writer
}

func newEnvRefreshAction(
	provisionManager *provisioning.Manager,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	env *environment.Environment,
	flags *envRefreshFlags,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &envRefreshAction{
		provisionManager: provisionManager,
		projectManager:   projectManager,
		env:              env,
		console:          console,
		flags:            flags,
		formatter:        formatter,
		projectConfig:    projectConfig,
		writer:           writer,
	}
}

func (ef *envRefreshAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if err := ef.projectManager.Initialize(ctx, ef.projectConfig); err != nil {
		return nil, err
	}

	if err := ef.provisionManager.Initialize(ctx, ef.projectConfig.Path, ef.projectConfig.Infra); err != nil {
		return nil, fmt.Errorf("initializing provisioning manager: %w", err)
	}

	getStateResult, err := ef.provisionManager.State(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting deployment: %w", err)
	}

	if err := provisioning.UpdateEnvironment(ef.env, getStateResult.State.Outputs); err != nil {
		return nil, err
	}

	ef.console.Message(ctx, "Environments setting refresh completed")

	if ef.formatter.Kind() == output.JsonFormat {
		err = ef.formatter.Format(provisioning.NewEnvRefreshResultFromState(getStateResult.State), ef.writer, nil)
		if err != nil {
			return nil, fmt.Errorf("writing deployment result in JSON format: %w", err)
		}
	}

	for _, svc := range ef.projectConfig.Services {
		eventArgs := project.ServiceLifecycleEventArgs{
			Project: ef.projectConfig,
			Service: svc,
			Args: map[string]any{
				"bicepOutput": getStateResult.State.Outputs,
			},
		}

		if err := svc.RaiseEvent(ctx, project.ServiceEventEnvUpdated, eventArgs); err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func newEnvGetValuesFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envGetValuesFlags {
	flags := &envGetValuesFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newEnvGetValuesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get-values",
		Short: "Get all environment values.",
	}
}

type envGetValuesFlags struct {
	envFlag
	global *internal.GlobalCommandOptions
}

func (eg *envGetValuesFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	eg.envFlag.Bind(local, global)
	eg.global = global
}

type envGetValuesAction struct {
	azdCtx    *azdcontext.AzdContext
	console   input.Console
	env       *environment.Environment
	formatter output.Formatter
	writer    io.Writer
	flags     *envGetValuesFlags
}

func newEnvGetValuesAction(
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	flags *envGetValuesFlags,
) actions.Action {
	return &envGetValuesAction{
		azdCtx:    azdCtx,
		console:   console,
		env:       env,
		formatter: formatter,
		writer:    writer,
		flags:     flags,
	}
}

func (eg *envGetValuesAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	err := eg.formatter.Format(eg.env.Dotenv(), eg.writer, nil)
	if err != nil {
		return nil, err
	}

	return nil, nil
}

func getCmdEnvHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Manage your application environments. With this command group, you can create a new environment or get, set,"+
			" and list your application environments.",
		[]string{
			formatHelpNote("An Application can have multiple environments (ex: dev, test, prod)."),
			formatHelpNote("Each environment may have a different configuration (that is, connectivity information)" +
				" for accessing Azure resources."),
			formatHelpNote(fmt.Sprintf("You can find all environment configuration under the %s folder.",
				output.WithLinkFormat(".azure/<environment-name>"))),
			formatHelpNote(fmt.Sprintf("The environment name is stored as the %s environment variable in the %s file.",
				output.WithHighLightFormat("AZURE_ENV_NAME"),
				output.WithLinkFormat(".azure/<environment-name>/.env"))),
		})
}
