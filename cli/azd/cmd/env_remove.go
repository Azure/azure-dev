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

func newEnvRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <environment>",
		Short: "Removes an environment.",

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
	global       *internal.GlobalCommandOptions
	removeLocal  bool
	removeRemote bool
	force        bool
}

func (er *envRemoveFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	er.EnvFlag.Bind(local, global)
	er.global = global
	local.BoolVar(&er.force, "force", false, "Skips confirmation before performing removal.")
	local.BoolVar(
		&er.removeLocal,
		"local",
		false,
		"Removes the environment locally without removing any remote resources.",
	)
	local.BoolVar(
		&er.removeRemote,
		"remote",
		false,
		"Removes the environment remotely only.",
	)
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
	if er.flags.removeLocal && er.flags.removeRemote {
		return nil, errors.New("cannot specify both --local and --remote")
	}

	// Command title
	er.console.MessageUxItem(ctx, &ux.MessageTitle{
		Title: "Removes an environment (azd env rm)",
		TitleNote: "Azure resources are not deleted when running 'azd env rm'." +
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
					"Are you sure you want to remove the environment '%s'?", env.Name),
			})
		if !confirm || err != nil {
			return nil, err
		}
	}

	var deleteRemote bool
	if er.flags.removeLocal {
		deleteRemote = false
	} else if er.flags.removeRemote {
		//TODO: Remove remote -- not local?
		deleteRemote = true
	} else if env.HasRemote {
		deleteRemote, err = er.console.Confirm(ctx,
			input.ConsoleOptions{
				Message: fmt.Sprintf(
					"This environment is stored remotely. Would you like to remove remote resources for '%s'?", env.Name),
				DefaultValue: true,
			})
		if err != nil {
			return nil, err
		}
	}

	deleteOptions := &environment.DeleteOptions{
		DeleteRemote: deleteRemote,
	}
	err = er.envManager.Delete(ctx, env.Name, deleteOptions)
	if err != nil {
		return nil, err
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header: fmt.Sprintf("Environment '%s' was removed.", env.Name),
		},
	}, nil
}
