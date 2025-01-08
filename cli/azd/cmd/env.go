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
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
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

	group.Add("get-value", &actions.ActionDescriptorOptions{
		Command:        newEnvGetValueCmd(),
		FlagsResolver:  newEnvGetValueFlags,
		ActionResolver: newEnvGetValueAction,
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
	internal.EnvFlag
	global *internal.GlobalCommandOptions
}

func (f *envSetFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global
}

type envSetAction struct {
	console    input.Console
	azdCtx     *azdcontext.AzdContext
	env        *environment.Environment
	envManager environment.Manager
	flags      *envSetFlags
	args       []string
}

func newEnvSetAction(
	azdCtx *azdcontext.AzdContext,
	env *environment.Environment,
	envManager environment.Manager,
	console input.Console,
	flags *envSetFlags,
	args []string,
) actions.Action {
	return &envSetAction{
		console:    console,
		azdCtx:     azdCtx,
		env:        env,
		envManager: envManager,
		flags:      flags,
		args:       args,
	}
}

func (e *envSetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	e.env.DotenvSet(e.args[0], e.args[1])

	if err := e.envManager.Save(ctx, e.env); err != nil {
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
	azdCtx     *azdcontext.AzdContext
	envManager environment.Manager
	args       []string
}

func newEnvSelectAction(azdCtx *azdcontext.AzdContext, envManager environment.Manager, args []string) actions.Action {
	return &envSelectAction{
		azdCtx:     azdCtx,
		envManager: envManager,
		args:       args,
	}
}

func (e *envSelectAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	_, err := e.envManager.Get(ctx, e.args[0])
	if errors.Is(err, environment.ErrNotFound) {
		return nil, fmt.Errorf(
			`environment '%s' does not exist. You can create it with "azd env new %s"`,
			e.args[0],
			e.args[0],
		)
	} else if err != nil {
		return nil, fmt.Errorf("ensuring environment exists: %w", err)
	}

	if err := e.azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: e.args[0]}); err != nil {
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
	envManager environment.Manager
	azdCtx     *azdcontext.AzdContext
	formatter  output.Formatter
	writer     io.Writer
}

func newEnvListAction(
	envManager environment.Manager,
	azdCtx *azdcontext.AzdContext,
	formatter output.Formatter,
	writer io.Writer,
) actions.Action {
	return &envListAction{
		envManager: envManager,
		azdCtx:     azdCtx,
		formatter:  formatter,
		writer:     writer,
	}
}

func (e *envListAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	envs, err := e.envManager.List(ctx)

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
			{
				Heading:       "LOCAL",
				ValueTemplate: "{{.HasLocal}}",
			},
			{
				Heading:       "REMOTE",
				ValueTemplate: "{{.HasRemote}}",
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
		Short: "Create a new environment and set it as the default.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
}

type envNewAction struct {
	azdCtx     *azdcontext.AzdContext
	envManager environment.Manager
	flags      *envNewFlags
	args       []string
	console    input.Console
}

func newEnvNewAction(
	azdCtx *azdcontext.AzdContext,
	envManager environment.Manager,
	flags *envNewFlags,
	args []string,
	console input.Console,
) actions.Action {
	return &envNewAction{
		azdCtx:     azdCtx,
		envManager: envManager,
		flags:      flags,
		args:       args,
		console:    console,
	}
}

func (en *envNewAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	environmentName := ""
	if len(en.args) >= 1 {
		environmentName = en.args[0]
	}

	envSpec := environment.Spec{
		Name:         environmentName,
		Subscription: en.flags.subscription,
		Location:     en.flags.location,
	}

	env, err := en.envManager.Create(ctx, envSpec)
	if err != nil {
		return nil, fmt.Errorf("creating new environment: %w", err)
	}

	if err := en.azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: env.Name()}); err != nil {
		return nil, fmt.Errorf("saving default environment: %w", err)
	}

	return nil, nil
}

type envRefreshFlags struct {
	hint   string
	global *internal.GlobalCommandOptions
	internal.EnvFlag
}

func (er *envRefreshFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.StringVarP(&er.hint, "hint", "", "", "Hint to help identify the environment to refresh")

	er.EnvFlag.Bind(local, global)
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

			if flagValue, err := cmd.Flags().GetString(internal.EnvironmentNameFlagName); err == nil {
				if flagValue != "" && args[0] != flagValue {
					return errors.New(
						"the --environment flag and an explicit environment name as an argument may not be used together")
				}
			}

			return cmd.Flags().Set(internal.EnvironmentNameFlagName, args[0])
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
	envManager       environment.Manager
	prompters        prompt.Prompter
	flags            *envRefreshFlags
	console          input.Console
	formatter        output.Formatter
	writer           io.Writer
	importManager    *project.ImportManager
}

func newEnvRefreshAction(
	provisionManager *provisioning.Manager,
	projectConfig *project.ProjectConfig,
	projectManager project.ProjectManager,
	env *environment.Environment,
	envManager environment.Manager,
	prompters prompt.Prompter,
	flags *envRefreshFlags,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	importManager *project.ImportManager,
) actions.Action {
	return &envRefreshAction{
		provisionManager: provisionManager,
		projectManager:   projectManager,
		env:              env,
		envManager:       envManager,
		prompters:        prompters,
		console:          console,
		flags:            flags,
		formatter:        formatter,
		projectConfig:    projectConfig,
		writer:           writer,
		importManager:    importManager,
	}
}

func (ef *envRefreshAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command title
	ef.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: fmt.Sprintf("Refreshing environment %s (azd env refresh)", ef.env.Name()),
	})

	if err := ef.projectManager.Initialize(ctx, ef.projectConfig); err != nil {
		return nil, err
	}

	if err := ef.projectManager.EnsureAllTools(ctx, ef.projectConfig, nil); err != nil {
		return nil, err
	}

	infra, err := ef.importManager.ProjectInfrastructure(ctx, ef.projectConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = infra.Cleanup() }()

	// env refresh supports "BYOI" infrastructure where bicep isn't available
	err = ef.provisionManager.Initialize(ctx, ef.projectConfig.Path, infra.Options)
	if errors.Is(err, bicep.ErrEnsureEnvPreReqBicepCompileFailed) {
		// If bicep is not available, we continue to prompt for subscription and location unfiltered
		err = provisioning.EnsureSubscriptionAndLocation(ctx, ef.envManager, ef.env, ef.prompters,
			func(_ account.Location) bool { return true })
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("initializing provisioning manager: %w", err)
	}
	// If resource group is defined within the project but not in the environment then
	// add it to the environment to support BYOI lookup scenarios like ADE
	// Infra providers do not currently have access to project configuration
	projectResourceGroup, _ := ef.projectConfig.ResourceGroupName.Envsubst(ef.env.Getenv)
	if _, has := ef.env.LookupEnv(environment.ResourceGroupEnvVarName); !has && projectResourceGroup != "" {
		ef.env.DotenvSet(environment.ResourceGroupEnvVarName, projectResourceGroup)
	}

	stateOptions := provisioning.NewStateOptions(ef.flags.hint)
	getStateResult, err := ef.provisionManager.State(ctx, stateOptions)
	if err != nil {
		return nil, fmt.Errorf("getting deployment: %w", err)
	}

	if err := ef.provisionManager.UpdateEnvironment(ctx, getStateResult.State.Outputs); err != nil {
		return nil, err
	}

	if ef.formatter.Kind() == output.JsonFormat {
		err = ef.formatter.Format(provisioning.NewEnvRefreshResultFromState(getStateResult.State), ef.writer, nil)
		if err != nil {
			return nil, fmt.Errorf("writing deployment result in JSON format: %w", err)
		}
	}

	servicesStable, err := ef.importManager.ServiceStable(ctx, ef.projectConfig)
	if err != nil {
		return nil, err
	}

	for _, svc := range servicesStable {
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

	localEnvPath := ef.envManager.EnvPath(ef.env)

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "Environment refresh completed",
			FollowUp: fmt.Sprintf("View environment variables at %s", output.WithHyperlink(localEnvPath, localEnvPath)),
		},
	}, nil
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
	internal.EnvFlag
	global *internal.GlobalCommandOptions
}

func (eg *envGetValuesFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	eg.EnvFlag.Bind(local, global)
	eg.global = global
}

type envGetValuesAction struct {
	azdCtx     *azdcontext.AzdContext
	console    input.Console
	envManager environment.Manager
	formatter  output.Formatter
	writer     io.Writer
	flags      *envGetValuesFlags
}

func newEnvGetValuesAction(
	azdCtx *azdcontext.AzdContext,
	envManager environment.Manager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	flags *envGetValuesFlags,
) actions.Action {
	return &envGetValuesAction{
		azdCtx:     azdCtx,
		console:    console,
		envManager: envManager,
		formatter:  formatter,
		writer:     writer,
		flags:      flags,
	}
}

func (eg *envGetValuesAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	name, err := eg.azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}
	// Note: if there is not an environment yet, GetDefaultEnvironmentName() returns empty string (not error)
	// and later, when envManager.Get() is called with the empty string, azd returns an error.
	// But if there is already an environment (default to be selected), azd must honor the --environment flag
	// over the default environment.
	if eg.flags.EnvironmentName != "" {
		name = eg.flags.EnvironmentName
	}
	env, err := eg.envManager.Get(ctx, name)
	if errors.Is(err, environment.ErrNotFound) {
		return nil, fmt.Errorf(
			`"environment does not exist. You can create it with "azd env new"`,
		)
	} else if err != nil {
		return nil, fmt.Errorf("ensuring environment exists: %w", err)
	}

	return nil, eg.formatter.Format(env.Dotenv(), eg.writer, nil)
}

func newEnvGetValueFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envGetValueFlags {
	flags := &envGetValueFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newEnvGetValueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get-value <keyName>",
		Short: "Get specific environment value.",
	}
	cmd.Args = cobra.MaximumNArgs(1)

	return cmd
}

type envGetValueFlags struct {
	internal.EnvFlag
	global *internal.GlobalCommandOptions
}

func (eg *envGetValueFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	eg.EnvFlag.Bind(local, global)
	eg.global = global
}

type envGetValueAction struct {
	azdCtx     *azdcontext.AzdContext
	console    input.Console
	envManager environment.Manager
	writer     io.Writer
	flags      *envGetValueFlags
	args       []string
}

func newEnvGetValueAction(
	azdCtx *azdcontext.AzdContext,
	envManager environment.Manager,
	console input.Console,
	writer io.Writer,
	flags *envGetValueFlags,
	args []string,

) actions.Action {
	return &envGetValueAction{
		azdCtx:     azdCtx,
		console:    console,
		envManager: envManager,
		writer:     writer,
		flags:      flags,
		args:       args,
	}
}

func (eg *envGetValueAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(eg.args) < 1 {
		return nil, fmt.Errorf("no key name provided")
	}

	keyName := eg.args[0]

	name, err := eg.azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}
	// Note: if there is not an environment yet, GetDefaultEnvironmentName() returns empty string (not error)
	// and later, when envManager.Get() is called with the empty string, azd returns an error.
	// But if there is already an environment (default to be selected), azd must honor the --environment flag
	// over the default environment.
	if eg.flags.EnvironmentName != "" {
		name = eg.flags.EnvironmentName
	}
	env, err := eg.envManager.Get(ctx, name)
	if errors.Is(err, environment.ErrNotFound) {
		return nil, fmt.Errorf(
			`environment '%s' does not exist. You can create it with "azd env new %s"`,
			name,
			name,
		)
	} else if err != nil {
		return nil, fmt.Errorf("ensuring environment exists: %w", err)
	}

	values := env.Dotenv()
	keyValue, exists := values[keyName]
	if !exists {
		return nil, fmt.Errorf("key '%s' not found in the environment values", keyName)
	}

	// Directly write the key value to the writer
	if _, err := fmt.Fprintln(eg.writer, keyValue); err != nil {
		return nil, fmt.Errorf("writing key value: %w", err)
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
