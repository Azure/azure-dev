// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/spf13/cobra"
)

func envCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "env",
		Short: "Manage environments.",
		Long: `Manage environments.

This command group allows you to create a new environment or to get, set and list your application environments. An application can have multiple environments, e.g., dev, test, prod, each with different configuration (i.e., connectivity information) for accessing Azure resources. 

You can find all environment configurations under the .azure\<environment-name> folder(s). The environment name is stored as the AZURE_ENV_NAME environment variable in .azure\<environment-name>\folder\.env. `,
	}

	root.Flags().BoolP("help", "h", false, "Help for "+root.Name())
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

func envSetCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	actionFn := func(ctx context.Context, _ *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
		askOne := makeAskOne(rootOptions.NoPrompt)
		azCli := commands.GetAzCliFromContext(ctx)

		if err := ensureProject(azdCtx.ProjectPath()); err != nil {
			return err
		}

		if err := tools.EnsureInstalled(ctx, azCli); err != nil {
			return err
		}

		env, err := loadOrInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, askOne)
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
		"Set a value in the environment",
		"",
	)
	cmd.Args = cobra.ExactArgs(2)
	return cmd
}

func envSelectCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	action := commands.ActionFunc(
		func(_ context.Context, _ *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
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
		"Set the default environment",
		"",
	)
	cmd.Args = cobra.ExactArgs(1)
	return cmd
}

func envListCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List environments",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := environment.NewAzdContext()
			if err != nil {
				return fmt.Errorf("failed to get the current directory: %w", err)
			}

			if err := ensureProject(ctx.ProjectPath()); err != nil {
				return err
			}

			formatter, err := output.GetFormatter(cmd)
			if err != nil {
				return err
			}

			envs, err := ctx.ListEnvironments()
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

				err = formatter.Format(envs, cmd.OutOrStdout(), output.TableFormatterOptions{
					Columns: columns,
				})
			} else {
				err = formatter.Format(envs, cmd.OutOrStdout(), nil)
			}
			if err != nil {
				return err
			}

			return nil
		},
	}
}

func envNewCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	actionFn := func(ctx context.Context, _ *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
		askOne := makeAskOne(rootOptions.NoPrompt)
		azCli := commands.GetAzCliFromContext(ctx)

		if err := ensureProject(azdCtx.ProjectPath()); err != nil {
			return err
		}

		if err := tools.EnsureInstalled(ctx, azCli); err != nil {
			return err
		}

		if len(args) == 1 {
			rootOptions.EnvironmentName = args[0]
		}

		if _, err := createAndInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, askOne); err != nil {
			return fmt.Errorf("creating new environment: %w", err)
		}

		if err := azdCtx.SetDefaultEnvironmentName(rootOptions.EnvironmentName); err != nil {
			return fmt.Errorf("saving default environment: %w", err)
		}

		return nil
	}
	cmd := commands.Build(
		commands.ActionFunc(actionFn),
		rootOptions,
		"new <environment>",
		"Create a new environment",
		"",
	)
	cmd.Args = cobra.MaximumNArgs(1)
	return cmd
}

func envRefreshCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	actionFn := func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *environment.AzdContext) error {
		azCli := commands.GetAzCliFromContext(ctx)
		bicepCli := tools.NewBicepCli(azCli)
		askOne := makeAskOne(rootOptions.NoPrompt)

		if err := ensureProject(azdCtx.ProjectPath()); err != nil {
			return err
		}

		if err := tools.EnsureInstalled(ctx, azCli, bicepCli); err != nil {
			return err
		}

		if err := ensureLoggedIn(ctx); err != nil {
			return fmt.Errorf("failed to ensure login: %w", err)
		}

		env, err := loadOrInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, askOne)
		if err != nil {
			return fmt.Errorf("loading environment: %w", err)
		}

		projectConfig, err := project.LoadProjectConfig(azdCtx.ProjectPath(), &env)
		if err != nil {
			return fmt.Errorf("loading project: %w", err)
		}

		infraProvider, err := provisioning.NewInfraProvider(&env, projectConfig.Path, projectConfig.Infra, azCli)
		if err != nil {
			return fmt.Errorf("creating infrastructure provider: %w", err)
		}

		template, err := infraProvider.Compile(ctx)
		if err != nil {
			return err
		}

		res, err := azCli.GetSubscriptionDeployment(ctx, env.GetSubscriptionId(), env.GetEnvName())
		if errors.Is(err, tools.ErrDeploymentNotFound) {
			return fmt.Errorf("no deployment for environment '%s' found. Have you run `infra create`?", rootOptions.EnvironmentName)
		} else if err != nil {
			return fmt.Errorf("fetching latest deployment: %w", err)
		}

		if err = provisioning.UpdateEnvironment(&env, &template.Outputs); err != nil {
			return err
		}

		formatter, err := output.GetFormatter(cmd)
		if err != nil {
			return err
		}
		if formatter.Kind() == output.JsonFormat {
			err = formatter.Format(res, cmd.OutOrStdout(), nil)
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
		"Refresh environment settings using information from previous infrastructure provision",
		"",
	)
}

func envGetValuesCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "get-values",
		Short: "Get all environment values",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			ctx = context.WithValue(ctx, environment.OptionsContextKey, rootOptions)

			askOne := makeAskOne(rootOptions.NoPrompt)
			azCli := commands.GetAzCliFromContext(ctx)

			azdCtx, err := environment.NewAzdContext()
			if err != nil {
				return fmt.Errorf("failed to get the current directory: %w", err)
			}

			if err := ensureProject(azdCtx.ProjectPath()); err != nil {
				return err
			}

			if err := tools.EnsureInstalled(ctx, azCli); err != nil {
				return err
			}

			formatter, err := output.GetFormatter(cmd)
			if err != nil {
				return err
			}

			env, err := loadOrInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, askOne)
			if err != nil {
				return err
			}

			err = formatter.Format(env.Values, cmd.OutOrStdout(), nil)
			if err != nil {
				return err
			}

			return nil
		},
	}
}
