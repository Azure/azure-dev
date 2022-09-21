// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func envCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "env",
		Short: "Manage environments.",
		Long: `Manage environments.

With this command group, you can create a new environment or get, set, and list your application environments. An application can have multiple environments (for example, dev, test, prod), each with a different configuration (that is, connectivity information) for accessing Azure resources. 

You can find all environment configurations under the *.azure\<environment-name>* folder. The environment name is stored as the AZURE_ENV_NAME environment variable in the *.azure\<environment-name>\folder\.env* file.`,
	}

	root.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", root.Name()))
	root.AddCommand(envSetCmd(rootOptions))
	root.AddCommand(envSelectCmd(rootOptions))
	root.AddCommand(envNewCmd(rootOptions))
	root.AddCommand(output.AddOutputParam(
		envListCmd(rootOptions),
		[]output.Format{output.JsonFormat, output.TableFormat},
		output.TableFormat,
	))
	root.AddCommand(output.AddOutputParam(
		envRefreshCmd(rootOptions),
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	))
	root.AddCommand(output.AddOutputParam(
		envGetValuesCmd(rootOptions),
		[]output.Format{output.JsonFormat, output.EnvVarsFormat},
		output.EnvVarsFormat,
	))

	return root
}

func envSetCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	actionFn := func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		azCli := azcli.GetAzCli(ctx)
		console := input.GetConsole(ctx)

		if err := ensureProject(azdCtx.ProjectPath()); err != nil {
			return err
		}

		if err := tools.EnsureInstalled(ctx, azCli); err != nil {
			return err
		}

		//lint:ignore SA4006 // We want ctx overridden here for future changes
		env, ctx, err := loadOrInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, console) //nolint:ineffassign,staticcheck
		if err != nil {
			return fmt.Errorf("loading environment: %w", err)
		}

		env.Values[args[0]] = args[1]

		if err := env.Save(); err != nil {
			return fmt.Errorf("saving environment: %w", err)
		}

		return nil
	}

	cmd := commands.Build(
		commands.ActionFunc(actionFn),
		rootOptions,
		"set <key> <value>",
		"Set a value in the environment.",
		nil,
	)
	cmd.Args = cobra.ExactArgs(2)
	return cmd
}

func envSelectCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	action := commands.ActionFunc(
		func(_ context.Context, _ *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
			if err := ensureProject(azdCtx.ProjectPath()); err != nil {
				return err
			}

			if err := azdCtx.SetDefaultEnvironmentName(args[0]); err != nil {
				return fmt.Errorf("setting default environment: %w", err)
			}

			return nil
		},
	)
	cmd := commands.Build(
		action,
		rootOptions,
		"select <environment>",
		"Set the default environment.",
		nil,
	)
	cmd.Args = cobra.ExactArgs(1)
	return cmd
}

func envListCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	action := commands.ActionFunc(
		func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
			if err := ensureProject(azdCtx.ProjectPath()); err != nil {
				return err
			}

			formatter := output.GetFormatter(ctx)
			writer := output.GetWriter(ctx)
			envs, err := azdCtx.ListEnvironments()

			if err != nil {
				return fmt.Errorf("listing environments: %w", err)
			}

			if formatter.Kind() == output.TableFormat {
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

				err = formatter.Format(envs, writer, output.TableFormatterOptions{
					Columns: columns,
				})
			} else {
				err = formatter.Format(envs, writer, nil)
			}
			if err != nil {
				return err
			}

			return nil
		})

	cmd := commands.Build(
		action,
		rootOptions,
		"list",
		"List environments",
		&commands.BuildOptions{
			Aliases: []string{"ls"},
		},
	)

	return cmd
}

func envNewCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	return commands.Build(
		&envNewAction{rootOptions: rootOptions},
		rootOptions,
		"new <environment>",
		"Create a new environment.",
		nil,
	)
}

type envNewAction struct {
	rootOptions  *internal.GlobalCommandOptions
	subscription string
	location     string
}

func (en *envNewAction) SetupFlags(persis *pflag.FlagSet, local *pflag.FlagSet) {
	local.StringVar(&en.subscription, "subscription", "", "Name or ID of an Azure subscription to use for the new environment")
	local.StringVarP(&en.location, "location", "l", "", "Azure location for the new environment")
}

func (en *envNewAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	azCli := azcli.GetAzCli(ctx)
	if err := tools.EnsureInstalled(ctx, azCli); err != nil {
		return err
	}

	console := input.GetConsole(ctx)
	envSpec := environmentSpec{
		environmentName: en.rootOptions.EnvironmentName,
		subscription:    en.subscription,
		location:        en.location,
	}
	if _, _, err := createAndInitEnvironment(ctx, &envSpec, azdCtx, console); err != nil {
		return fmt.Errorf("creating new environment: %w", err)
	}

	if err := azdCtx.SetDefaultEnvironmentName(envSpec.environmentName); err != nil {
		return fmt.Errorf("saving default environment: %w", err)
	}

	return nil
}

func envRefreshCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	actionFn := func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		azCli := azcli.GetAzCli(ctx)
		console := input.GetConsole(ctx)

		if err := ensureProject(azdCtx.ProjectPath()); err != nil {
			return err
		}

		if err := tools.EnsureInstalled(ctx, azCli); err != nil {
			return err
		}

		if err := ensureLoggedIn(ctx); err != nil {
			return fmt.Errorf("failed to ensure login: %w", err)
		}

		env, ctx, err := loadOrInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, console)
		if err != nil {
			return fmt.Errorf("loading environment: %w", err)
		}

		prj, err := project.LoadProjectConfig(azdCtx.ProjectPath(), env)
		if err != nil {
			return fmt.Errorf("loading project: %w", err)
		}

		formatter := output.GetFormatter(ctx)
		writer := output.GetWriter(ctx)

		infraManager, err := provisioning.NewManager(ctx, env, prj.Path, prj.Infra, !rootOptions.NoPrompt)
		if err != nil {
			return fmt.Errorf("creating provisioning manager: %w", err)
		}

		scope := infra.NewSubscriptionScope(ctx, env.GetLocation(), env.GetSubscriptionId(), env.GetEnvName())

		getDeploymentResult, err := infraManager.GetDeployment(ctx, scope)
		if err != nil {
			return fmt.Errorf("getting deployment: %w", err)
		}

		if err := provisioning.UpdateEnvironment(env, &getDeploymentResult.Deployment.Outputs); err != nil {
			return err
		}

		console.Message(ctx, "Environments setting refresh completed")

		if formatter.Kind() == output.JsonFormat {
			err = formatter.Format(getDeploymentResult.Deployment, writer, nil)
			if err != nil {
				return fmt.Errorf("writing deployment result in JSON format: %w", err)
			}
		}

		return nil
	}

	return commands.Build(
		commands.ActionFunc(actionFn),
		rootOptions,
		"refresh",
		"Refresh environment settings by using information from a previous infrastructure provision.",
		nil,
	)
}

func envGetValuesCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	actionFn := commands.ActionFunc(
		func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
			console := input.GetConsole(ctx)
			azCli := azcli.GetAzCli(ctx)

			if err := ensureProject(azdCtx.ProjectPath()); err != nil {
				return err
			}

			if err := tools.EnsureInstalled(ctx, azCli); err != nil {
				return err
			}

			formatter := output.GetFormatter(ctx)
			writer := output.GetWriter(ctx)

			//lint:ignore SA4006 // We want ctx overridden here for future changes
			env, ctx, err := loadOrInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, console) //nolint:ineffassign,staticcheck
			if err != nil {
				return err
			}

			err = formatter.Format(env.Values, writer, nil)
			if err != nil {
				return err
			}

			return nil
		})

	cmd := commands.Build(
		actionFn,
		rootOptions,
		"get-values",
		"Get all environment values.",
		nil,
	)

	return cmd
}
