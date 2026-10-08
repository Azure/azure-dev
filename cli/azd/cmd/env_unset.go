// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newEnvUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key...>",
		Short: "Remove one or more environment values.",
		Long: "Remove one or more values from the selected environment's .env file.\n\n" +
			"Keys are case-sensitive. Missing keys are ignored.\n" +
			"Removing a Key Vault secret reference does not delete the secret.\n" +
			"Process environment variables and configuration values in config.json are not removed.",
		Example: "$ azd env unset MY_KEY\n$ azd env unset KEY1 KEY2 --environment dev",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.MinimumNArgs(1)(cmd, args); err != nil {
				return err
			}
			if slices.Contains(args, "") {
				return errors.New("environment variable key must not be empty")
			}
			return nil
		},
		Annotations: map[string]string{
			"azdtest.use": "unset key",
		},
	}
}

func getCmdEnvUnsetHelpDescription(cmd *cobra.Command) string {
	return generateCmdHelpDescription(cmd.Long, nil)
}

type envUnsetFlags struct {
	internal.EnvFlag
	global *internal.GlobalCommandOptions
}

func newEnvUnsetFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envUnsetFlags {
	flags := &envUnsetFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *envUnsetFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global
}

type envUnsetAction struct {
	env        *lazy.Lazy[*environment.Environment]
	envManager environment.Manager
	args       []string
}

func newEnvUnsetAction(
	env *lazy.Lazy[*environment.Environment],
	envManager environment.Manager,
	args []string,
) actions.Action {
	return &envUnsetAction{
		env:        env,
		envManager: envManager,
		args:       args,
	}
}

func (a *envUnsetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	env, err := a.env.GetValue()
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	for _, key := range a.args {
		env.DotenvDelete(key)
	}

	if err := a.envManager.Save(ctx, env); err != nil {
		return nil, fmt.Errorf("saving environment: %w", err)
	}

	return nil, nil
}
