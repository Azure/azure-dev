// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/iac/bicep"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	bicepTool "github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func envCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
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

func envSetCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	actionFn := func(ctx context.Context, _ *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		console := input.NewConsole(!rootOptions.NoPrompt)
		azCli := commands.GetAzCliFromContext(ctx)

		if err := ensureProject(azdCtx.ProjectPath()); err != nil {
			return err
		}

		if err := tools.EnsureInstalled(ctx, azCli); err != nil {
			return err
		}

		env, err := loadOrInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, console)
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
		"",
	)
	cmd.Args = cobra.ExactArgs(2)
	return cmd
}

func envSelectCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
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
		"",
	)
	cmd.Args = cobra.ExactArgs(1)
	return cmd
}

func envListCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List environments.",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := azdcontext.NewAzdContext()
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
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	return cmd
}

func envNewCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&envNewAction{rootOptions: rootOptions},
		rootOptions,
		"new <environment>",
		"Create a new environment.",
		"",
	)
	return cmd
}

type envNewAction struct {
	rootOptions  *commands.GlobalCommandOptions
	subscription string
	location     string
}

func (en *envNewAction) SetupFlags(persis *pflag.FlagSet, local *pflag.FlagSet) {
	local.StringVar(&en.subscription, "subscription", "", "Name or ID of an Azure subscription to use for the new environment")
	local.StringVarP(&en.location, "location", "l", "", "Azure location for the new environment")
}

func (en *envNewAction) Run(ctx context.Context, _ *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	if err := ensureProject(azdCtx.ProjectPath()); err != nil {
		return err
	}

	azCli := commands.GetAzCliFromContext(ctx)
	if err := tools.EnsureInstalled(ctx, azCli); err != nil {
		return err
	}

	console := input.NewConsole(!en.rootOptions.NoPrompt)
	envSpec := environmentSpec{
		environmentName: en.rootOptions.EnvironmentName,
		subscription:    en.subscription,
		location:        en.location,
	}
	if _, err := createAndInitEnvironment(ctx, &envSpec, azdCtx, console); err != nil {
		return fmt.Errorf("creating new environment: %w", err)
	}

	if err := azdCtx.SetDefaultEnvironmentName(envSpec.environmentName); err != nil {
		return fmt.Errorf("saving default environment: %w", err)
	}

	return nil
}

func envRefreshCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	actionFn := func(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
		azCli := commands.GetAzCliFromContext(ctx)
		bicepCli := bicepTool.NewBicepCli(bicepTool.NewBicepCliArgs{AzCli: azCli})
		console := input.NewConsole(!rootOptions.NoPrompt)

		if err := ensureProject(azdCtx.ProjectPath()); err != nil {
			return err
		}

		if err := tools.EnsureInstalled(ctx, azCli, bicepCli); err != nil {
			return err
		}

		if err := ensureLoggedIn(ctx); err != nil {
			return fmt.Errorf("failed to ensure login: %w", err)
		}

		env, err := loadOrInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, console)
		if err != nil {
			return fmt.Errorf("loading environment: %w", err)
		}

		template, err := bicep.Compile(ctx, bicepCli, filepath.Join(azdCtx.InfrastructureDirectory(), "main.bicep"))
		if err != nil {
			return err
		}

		res, err := azCli.GetSubscriptionDeployment(ctx, env.GetSubscriptionId(), env.GetEnvName())
		if errors.Is(err, azcli.ErrDeploymentNotFound) {
			return fmt.Errorf("no deployment for environment '%s' found. Have you run `infra create`?", rootOptions.EnvironmentName)
		} else if err != nil {
			return fmt.Errorf("fetching latest deployment: %w", err)
		}

		template.CanonicalizeDeploymentOutputs(&res.Properties.Outputs)
		if err = saveEnvironmentValues(res, env); err != nil {
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
		"Refresh environment settings by using information from a previous infrastructure provision.",
		"",
	)
}

func envGetValuesCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get-values",
		Short: "Get all environment values.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			ctx = commands.WithGlobalCommandOptions(ctx, rootOptions)

			console := input.NewConsole(!rootOptions.NoPrompt)
			azCli := commands.GetAzCliFromContext(ctx)

			azdCtx, err := azdcontext.NewAzdContext()
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

			env, err := loadOrInitEnvironment(ctx, &rootOptions.EnvironmentName, azdCtx, console)
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
	cmd.Flags().BoolP("help", "h", false, fmt.Sprintf("Gets help for %s.", cmd.Name()))
	return cmd
}
