// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newEnvUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset <key...>",
		Short: "Remove one or more environment values.",
		Long: "Remove one or more values from the selected environment's .env file.\n\n" +
			"Keys are case-sensitive. Missing keys are ignored with a warning.\n" +
			"You are prompted to confirm removal. Use --force to skip this confirmation.\n" +
			"If saving fails after local values change, you can choose to restore the previous values.\n" +
			"With --force, restoration is attempted automatically without prompting.\n" +
			"Removing a Key Vault secret reference does not delete the secret.\n" +
			"Configuration values in config.json are not removed.",
		Example: "$ azd env unset MY_KEY\n$ azd env unset KEY1 KEY2 --environment dev\n" +
			"$ azd env unset MY_KEY --force",
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
	force  bool
}

func newEnvUnsetFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *envUnsetFlags {
	flags := &envUnsetFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *envUnsetFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.EnvFlag.Bind(local, global)
	f.global = global
	local.BoolVar(&f.force, "force", false,
		"Skips removal confirmation and automatically attempts restoration if saving fails.")
}

type envUnsetAction struct {
	env        *lazy.Lazy[*environment.Environment]
	envManager environment.Manager
	console    input.Console
	flags      *envUnsetFlags
	args       []string
}

func newEnvUnsetAction(
	env *lazy.Lazy[*environment.Environment],
	envManager environment.Manager,
	console input.Console,
	flags *envUnsetFlags,
	args []string,
) actions.Action {
	return &envUnsetAction{
		env:        env,
		envManager: envManager,
		console:    console,
		flags:      flags,
		args:       args,
	}
}

func (a *envUnsetAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	env, err := a.env.GetValue()
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	if err := a.envManager.Reload(ctx, env); err != nil {
		return nil, fmt.Errorf("reloading environment before removal: %w", err)
	}
	keys, previousValues := a.valuesToRemove(ctx, env, a.args)
	if len(keys) == 0 {
		return nil, nil
	}

	if !a.flags.force {
		if a.console.IsNoPromptMode() {
			return nil, &internal.ErrorWithSuggestion{
				Err:        errors.New("removing environment values requires confirmation"),
				Message:    "Removing environment values requires confirmation. No values were removed.",
				Suggestion: "Run the command again with --force to skip removal confirmation.",
			}
		}
		valueLabel := "Environment values"
		if len(keys) == 1 {
			valueLabel = "Environment value"
		}
		confirmed, err := a.console.Confirm(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf(
				"%s %s will be removed from environment %q. Do you want to continue?",
				valueLabel, formatEnvUnsetKeys(keys), env.Name()),
			DefaultValue: false,
		})
		if err != nil {
			return nil, fmt.Errorf("confirming environment value removal: %w", err)
		}
		if !confirmed {
			a.console.Message(ctx, "No environment values were removed.")
			return nil, nil
		}

		// Refresh after the prompt so the snapshot and save do not replay stale values from before confirmation.
		if err := a.envManager.Reload(ctx, env); err != nil {
			return nil, fmt.Errorf("reloading environment after confirmation: %w", err)
		}
		keys, previousValues = a.valuesToRemove(ctx, env, keys)
		if len(keys) == 0 {
			return nil, nil
		}
	}

	for _, key := range keys {
		env.DotenvDelete(key)
	}

	if err := a.envManager.Save(ctx, env); err != nil {
		return nil, a.handleSaveError(ctx, env, keys, previousValues, err)
	}

	return nil, nil
}

func (a *envUnsetAction) valuesToRemove(
	ctx context.Context, env *environment.Environment, args []string,
) ([]string, map[string]string) {
	keys := make([]string, 0, len(args))
	values := make(map[string]string, len(args))
	seen := make(map[string]struct{}, len(args))
	for _, key := range args {
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		value, exists := env.LookupDotenv(key)
		if !exists {
			a.console.Message(ctx, output.WithWarningFormat(fmt.Sprintf(
				"WARNING: Environment value %q was not found in environment %q and was ignored.", key, env.Name())))
			continue
		}
		keys = append(keys, key)
		values[key] = value
	}
	return keys, values
}

func (a *envUnsetAction) handleSaveError(
	ctx context.Context,
	env *environment.Environment,
	keys []string,
	previousValues map[string]string,
	saveErr error,
) error {
	saveErr = fmt.Errorf("saving environment %q: %w", env.Name(), saveErr)
	inspectionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	err := a.envManager.Reload(inspectionCtx, env)
	cancel()
	if err != nil {
		return errors.Join(saveErr, fmt.Errorf(
			"could not determine whether local .env values changed; no restoration was attempted: %w", err))
	}

	changed := false
	for key, previousValue := range previousValues {
		if value, exists := env.LookupDotenv(key); !exists || value != previousValue {
			changed = true
			break
		}
	}
	if !changed {
		return fmt.Errorf("%w; the selected local .env values were not modified", saveErr)
	}
	if !a.flags.force {
		if a.console.IsNoPromptMode() {
			return fmt.Errorf(
				"%w; local .env values changed and were not restored because confirmation is required", saveErr)
		}

		confirmed, err := a.console.Confirm(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf(
				"Saving environment %q failed and its local .env values changed. Restore the previous values for %s?",
				env.Name(), formatEnvUnsetKeys(keys)),
			DefaultValue: false,
		})
		if err != nil {
			return errors.Join(saveErr, fmt.Errorf(
				"local .env values changed and were not restored; confirming restoration: %w", err))
		}
		if !confirmed {
			return fmt.Errorf("%w; local .env values changed and were not restored", saveErr)
		}
	}

	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	// Preserve unrelated updates made while the user was deciding whether to restore.
	if err := a.envManager.Reload(restoreCtx, env); err != nil {
		return errors.Join(saveErr, fmt.Errorf(
			"local .env values changed and were not restored; reloading before restoration: %w", err))
	}
	for key, value := range previousValues {
		env.DotenvSet(key, value)
	}
	if err := a.envManager.Save(restoreCtx, env); err != nil {
		return errors.Join(saveErr, fmt.Errorf("restoring previous environment values failed: %w", err))
	}
	return fmt.Errorf("%w; the previous local .env values were restored", saveErr)
}

func formatEnvUnsetKeys(keys []string) string {
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = fmt.Sprintf("%q", key)
	}
	return strings.Join(quoted, ", ")
}
