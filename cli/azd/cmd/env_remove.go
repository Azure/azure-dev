// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func getCmdEnvRemoveHelpDescription(*cobra.Command) string {
	return generateCmdHelpDescription(
		"Removes an environment and its local configuration.",
		[]string{
			formatHelpNote("This command only deletes local files stored in .azure/<environment>."),
			formatHelpNote("It does not delete any Azure resources. To delete Azure resources, run 'azd down' first."),
		})
}

func newEnvRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remove <environment>",
		Short:   "Remove an environment.",
		Aliases: []string{"rm"},

		// We want to support the usual -e / --environment arguments as all our commands which take environments do, but for
		// ergonomics, we'd also like you to be able to run `azd env rm some-environment-name` to behave the same way as
		// `azd env rm -e some-environment-name` would have.
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
	}
	return cmd
}

type envRemoveFlags struct {
	internal.EnvFlag
	global *internal.GlobalCommandOptions
	force  bool
}

func (er *envRemoveFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	er.EnvFlag.Bind(local, global)
	er.global = global
	local.BoolVar(&er.force, "force", false, "Skips confirmation before performing removal.")
}

func newEnvRemoveFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envRemoveFlags {
	flags := &envRemoveFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

type envRemoveAction struct {
	azdCtx     *azdcontext.AzdContext
	console    input.Console
	envManager environment.Manager
	formatter  output.Formatter
	writer     io.Writer
	flags      *envRemoveFlags
	args       []string
}

func newEnvRemoveAction(
	azdCtx *azdcontext.AzdContext,
	envManager environment.Manager,
	console input.Console,
	formatter output.Formatter,
	writer io.Writer,
	flags *envRemoveFlags,
	args []string,
) actions.Action {
	return &envRemoveAction{
		azdCtx:     azdCtx,
		console:    console,
		envManager: envManager,
		formatter:  formatter,
		writer:     writer,
		flags:      flags,
		args:       args,
	}
}

func (er *envRemoveAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	// Command title
	er.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Remove an environment (azd env remove)",
		TitleNote: "Azure resources are not deleted when running 'azd env remove'." +
			" To delete Azure resources, run 'azd down' instead.",
	})

	name, err := er.azdCtx.GetDefaultEnvironmentName()
	if err != nil {
		return nil, err
	}

	if er.flags.EnvironmentName != "" {
		name = er.flags.EnvironmentName
	}

	if name == "" {
		return nil, fmt.Errorf("no environment specified")
	}

	envs, err := er.envManager.List(ctx)
	if err != nil {
		return nil, err
	}

	idx := slices.IndexFunc(envs, func(env *environment.Description) bool {
		return env.Name == name
	})

	if idx < 0 {
		return nil, fmt.Errorf("environment '%s' does not exist", name)
	}

	env := envs[idx]
	if !er.flags.force {
		confirm, err := er.console.Confirm(
			ctx,
			input.ConsoleOptions{
				Message: fmt.Sprintf(
					"Remove the environment '%s'?", env.Name),
			})
		if !confirm || err != nil {
			return nil, err
		}
	}

	err = er.envManager.Delete(ctx, env.Name)
	if err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Environment '%s' was removed.", env.Name),
		},
	}, nil
}
