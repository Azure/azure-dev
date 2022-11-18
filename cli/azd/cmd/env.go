// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func envCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "env",
		Short: "Manage environments.",
		//nolint:lll
		Long: `Manage environments.

With this command group, you can create a new environment or get, set, and list your application environments. An application can have multiple environments (for example, dev, test, prod), each with a different configuration (that is, connectivity information) for accessing Azure resources. 

You can find all environment configurations under the *.azure\<environment-name>* folder. The environment name is stored as the AZURE_ENV_NAME environment variable in the *.azure\<environment-name>\folder\.env* file.`,
	}

	root.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", root.Name()))
	root.AddCommand(BuildCmd(rootOptions, envSetCmdDesign, initEnvSetAction, nil))
	root.AddCommand(BuildCmd(rootOptions, envSelectCmdDesign, initEnvSelectAction, nil))
	root.AddCommand(BuildCmd(rootOptions, envNewCmdDesign, initEnvNewAction, nil))
	root.AddCommand(BuildCmd(rootOptions, envListCmdDesign, initEnvListAction, nil))
	root.AddCommand(BuildCmd(rootOptions, envRefreshCmdDesign, initEnvRefreshAction, nil))
	root.AddCommand(BuildCmd(rootOptions, envGetValuesDesign, initEnvGetValuesAction, nil))

	return root
}

func envSetCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *envSetFlags) {
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a value in the environment.",
	}
	cmd.Args = cobra.ExactArgs(2)
	envSetFlags := &envSetFlags{}
	envSetFlags.Bind(cmd.Flags(), global)
	return cmd, envSetFlags
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
	azCli   azcli.AzCli
	console input.Console
	azdCtx  *azdcontext.AzdContext
	flags   envSetFlags
	args    []string
}

func newEnvSetAction(
	azdCtx *azdcontext.AzdContext,
	azCli azcli.AzCli,
	console input.Console,
	flags envSetFlags,
	args []string,
) *envSetAction {
	return &envSetAction{
		azCli:   azCli,
		console: console,
		azdCtx:  azdCtx,
		flags:   flags,
		args:    args,
	}
}

func (e *envSetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if err := ensureProject(e.azdCtx.ProjectPath()); err != nil {
		return nil, err
	}

	//lint:ignore SA4006 // We want ctx overridden here for future changes
	env, ctx, err := loadOrInitEnvironment( //nolint:ineffassign,staticcheck
		ctx,
		&e.flags.environmentName,
		e.azdCtx,
		e.console,
		e.azCli,
	)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	env.Values[e.args[0]] = e.args[1]

	if err := env.Save(); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return nil, nil
}

func envSelectCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:   "select <environment>",
		Short: "Set the default environment.",
	}
	cmd.Args = cobra.ExactArgs(1)
	return cmd, &struct{}{}
}

type envSelectAction struct {
	azdCtx *azdcontext.AzdContext
	args   []string
}

func newEnvSelectAction(azdCtx *azdcontext.AzdContext, args []string) *envSelectAction {
	return &envSelectAction{
		azdCtx: azdCtx,
		args:   args,
	}
}

func (e *envSelectAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if err := ensureProject(e.azdCtx.ProjectPath()); err != nil {
		return nil, err
	}

	if err := e.azdCtx.SetDefaultEnvironmentName(e.args[0]); err != nil {
		return nil, fmt.Errorf("setting default environment: %w", err)
	}

	return nil, nil
}

func envListCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *struct{}) {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List environments",
		Aliases: []string{"ls"},
	}
	output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat, output.TableFormat},
		output.TableFormat,
	)
	return cmd, &struct{}{}
}

type envListAction struct {
	azdCtx    *azdcontext.AzdContext
	formatter output.Formatter
	writer    io.Writer
}

func newEnvListAction(azdCtx *azdcontext.AzdContext, formatter output.Formatter, writer io.Writer) *envListAction {
	return &envListAction{
		azdCtx:    azdCtx,
		formatter: formatter,
		writer:    writer,
	}
}

func (e *envListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if err := ensureProject(e.azdCtx.ProjectPath()); err != nil {
		return nil, err
	}

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

func envNewCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *envNewFlags) {
	cmd := &cobra.Command{
		Use:   "new <environment>",
		Short: "Create a new environment.",
	}
	f := &envNewFlags{}
	f.Bind(cmd.Flags(), global)
	cmd.Args = cobra.MaximumNArgs(1)
	return cmd, f
}

type envNewAction struct {
	azdCtx  *azdcontext.AzdContext
	azCli   azcli.AzCli
	flags   envNewFlags
	args    []string
	console input.Console
}

func newEnvNewAction(
	azdCtx *azdcontext.AzdContext,
	azcli azcli.AzCli,
	flags envNewFlags,
	args []string,
	console input.Console,
) *envNewAction {
	return &envNewAction{
		azdCtx:  azdCtx,
		azCli:   azcli,
		flags:   flags,
		args:    args,
		console: console,
	}
}

func (en *envNewAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if err := ensureProject(en.azdCtx.ProjectPath()); err != nil {
		return nil, err
	}

	environmentName := ""
	if len(en.args) >= 1 {
		environmentName = en.args[0]
	}

	envSpec := environmentSpec{
		environmentName: environmentName,
		subscription:    en.flags.subscription,
		location:        en.flags.location,
	}
	if _, _, err := createAndInitEnvironment(ctx, &envSpec, en.azdCtx, en.console, en.azCli); err != nil {
		return nil, fmt.Errorf("creating new environment: %w", err)
	}

	if err := en.azdCtx.SetDefaultEnvironmentName(envSpec.environmentName); err != nil {
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

func envRefreshCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *envRefreshFlags) {
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh environment settings by using information from a previous infrastructure provision.",
	}

	output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)

	envRefreshFlags := &envRefreshFlags{}
	envRefreshFlags.Bind(cmd.Flags(), global)
	return cmd, envRefreshFlags
}

type envRefreshAction struct {
	azdCtx        *azdcontext.AzdContext
	azCli         azcli.AzCli
	flags         envRefreshFlags
	console       input.Console
	formatter     output.Formatter
	writer        io.Writer
	commandRunner exec.CommandRunner
}

func newEnvRefreshAction(
	azdCtx *azdcontext.AzdContext,
	azCli azcli.AzCli,
	commandRunner exec.CommandRunner,
	flags envRefreshFlags,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
) *envRefreshAction {
	return &envRefreshAction{
		azdCtx:        azdCtx,
		azCli:         azCli,
		flags:         flags,
		console:       console,
		formatter:     formatter,
		writer:        writer,
		commandRunner: commandRunner,
	}
}

func (ef *envRefreshAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if err := ensureProject(ef.azdCtx.ProjectPath()); err != nil {
		return nil, err
	}

	env, ctx, err := loadOrInitEnvironment(ctx, &ef.flags.environmentName, ef.azdCtx, ef.console, ef.azCli)
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	prj, err := project.LoadProjectConfig(ef.azdCtx.ProjectPath(), env)
	if err != nil {
		return nil, fmt.Errorf("loading project: %w", err)
	}

	infraManager, err := provisioning.NewManager(
		ctx, env, prj.Path, prj.Infra, !ef.flags.global.NoPrompt, ef.azCli, ef.console, ef.commandRunner,
	)
	if err != nil {
		return nil, fmt.Errorf("creating provisioning manager: %w", err)
	}

	scope := infra.NewSubscriptionScope(ef.azCli, env.GetLocation(), env.GetSubscriptionId(), env.GetEnvName())

	getStateResult, err := infraManager.State(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("getting deployment: %w", err)
	}

	if err := provisioning.UpdateEnvironment(env, getStateResult.State.Outputs); err != nil {
		return nil, err
	}

	ef.console.Message(ctx, "Environments setting refresh completed")

	if ef.formatter.Kind() == output.JsonFormat {
		err = ef.formatter.Format(provisioning.NewEnvRefreshResultFromState(getStateResult.State), ef.writer, nil)
		if err != nil {
			return nil, fmt.Errorf("writing deployment result in JSON format: %w", err)
		}
	}

	if err = prj.Initialize(ctx, env, ef.commandRunner); err != nil {
		return nil, err
	}

	for _, svc := range prj.Services {
		if err := svc.RaiseEvent(
			ctx, project.EnvironmentUpdated,
			map[string]any{"bicepOutput": getStateResult.State.Outputs}); err != nil {
			return nil, err
		}
	}

	return nil, nil
}

func envGetValuesDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *envGetValuesFlags) {
	cmd := &cobra.Command{
		Use:   "get-values",
		Short: "Get all environment values.",
	}

	output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat, output.EnvVarsFormat},
		output.EnvVarsFormat,
	)

	envGetValuesFlags := &envGetValuesFlags{}
	envGetValuesFlags.Bind(cmd.Flags(), global)
	return cmd, envGetValuesFlags
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
	formatter output.Formatter
	writer    io.Writer
	azCli     azcli.AzCli
	flags     envGetValuesFlags
}

func newEnvGetValuesAction(
	azdCtx *azdcontext.AzdContext,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	azCli azcli.AzCli,
	flags envGetValuesFlags,
) *envGetValuesAction {
	return &envGetValuesAction{
		azdCtx:    azdCtx,
		console:   console,
		formatter: formatter,
		writer:    writer,
		azCli:     azCli,
		flags:     flags,
	}
}

func (eg *envGetValuesAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if err := ensureProject(eg.azdCtx.ProjectPath()); err != nil {
		return nil, err
	}

	//lint:ignore SA4006 // We want ctx overridden here for future changes
	env, ctx, err := loadOrInitEnvironment( //nolint:ineffassign,staticcheck
		ctx,
		&eg.flags.environmentName,
		eg.azdCtx,
		eg.console,
		eg.azCli,
	)
	if err != nil {
		return nil, err
	}

	err = eg.formatter.Format(env.Values, eg.writer, nil)
	if err != nil {
		return nil, err
	}

	return nil, nil
}
