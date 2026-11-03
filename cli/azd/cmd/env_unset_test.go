// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/joho/godotenv"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestEnvUnsetCmd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		args          []string
		errorContains string
	}{
		{name: "SingleKey", args: []string{"KEY"}},
		{name: "MultipleKeys", args: []string{"KEY1", "KEY2"}},
		{name: "RepeatedKeys", args: []string{"KEY", "KEY"}},
		{name: "NoKeys", errorContains: "requires at least 1 arg(s)"},
		{name: "EmptyKey", args: []string{""}, errorContains: "key must not be empty"},
		{name: "EmptyKeyAfterValidKey", args: []string{"KEY", ""}, errorContains: "key must not be empty"},
		{
			name:          "ManagedKey",
			args:          []string{environment.EnvNameEnvVarName},
			errorContains: "cannot unset AZURE_ENV_NAME: this command removes .env keys, not the environment name",
		},
		{
			name:          "ManagedKeyAfterValidKey",
			args:          []string{"KEY", environment.EnvNameEnvVarName},
			errorContains: "cannot unset AZURE_ENV_NAME: this command removes .env keys, not the environment name",
		},
		{
			name:          "ManagedKeyBeforeValidKey",
			args:          []string{environment.EnvNameEnvVarName, "KEY"},
			errorContains: "cannot unset AZURE_ENV_NAME: this command removes .env keys, not the environment name",
		},
		{
			name:          "RepeatedManagedKey",
			args:          []string{environment.EnvNameEnvVarName, environment.EnvNameEnvVarName},
			errorContains: "cannot unset AZURE_ENV_NAME: this command removes .env keys, not the environment name",
		},
		{name: "ManagedKeyDifferentCase", args: []string{strings.ToLower(environment.EnvNameEnvVarName)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := newEnvUnsetCmd()
			require.Equal(t, "unset <key...>", cmd.Use)
			require.Contains(t, cmd.Short, "Remove one or more keys from an environment.")
			require.Contains(t, cmd.Short, "(Beta)")
			require.NotEmpty(t, cmd.Example)
			require.Contains(t, getCmdEnvUnsetHelpDescription(cmd), "(Beta)")
			require.Contains(t, getCmdEnvUnsetHelpDescription(cmd), "config.json")
			require.Contains(t, getCmdEnvUnsetHelpDescription(cmd), "does not delete the secret")
			require.Contains(t, cmd.Long, "ignored with a warning")
			require.Contains(t, cmd.Long, "AZURE_ENV_NAME identifies the environment and cannot be unset.")
			require.NotContains(t, cmd.Long, "managed by azd")
			require.Contains(t, cmd.Long, "confirm removal (defaults to Yes)")
			require.Contains(t, cmd.Long, "--force")
			require.Contains(t, cmd.Long, "restore the previous values (defaults to Yes)")
			require.Contains(t, cmd.Long, "With --force, restoration is attempted automatically without prompting.")
			require.NotContains(t, cmd.Long, "Process environment variables")
			require.NotContains(t, cmd.Long, "Managed values")

			require.Equal(t, output.WithErrorFormat("ERROR:"), cmd.ErrPrefix())
			require.NoError(t, cmd.Args(cmd, tt.args))
			err := validateEnvUnsetKeys(tt.args)
			if tt.errorContains != "" {
				require.ErrorContains(t, err, tt.errorContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestEnvUnsetHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := newEnvUnsetCmd()
	footer := getCmdEnvUnsetHelpFooter(cmd)
	require.Contains(t, footer, "Examples")
	for example := range strings.SplitSeq(cmd.Example, "\n") {
		require.Contains(t, footer, strings.TrimPrefix(example, "$ "))
	}
	require.NotContains(t, footer, "--no-prompt")
}

func TestEnvUnsetFlags(t *testing.T) {
	t.Setenv(environment.EnvNameEnvVarName, "process-env")

	tests := []struct {
		name        string
		args        []string
		environment string
		force       bool
	}{
		{name: "ProcessDefault", environment: "process-env"},
		{name: "LongFlag", args: []string{"--environment", "target-env"}, environment: "target-env"},
		{name: "ShortFlag", args: []string{"-e", "target-env"}, environment: "target-env"},
		{name: "Force", args: []string{"--force"}, environment: "process-env", force: true},
		{name: "ForceFalse", args: []string{"--force=false"}, environment: "process-env"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newEnvUnsetCmd()
			global := &internal.GlobalCommandOptions{}
			flags := newEnvUnsetFlags(cmd, global)
			require.NoError(t, cmd.Flags().Parse(tt.args))
			require.Equal(t, tt.environment, flags.EnvironmentName)
			require.Same(t, global, flags.global)
			require.Equal(t, tt.force, flags.force)
			require.Nil(t, cmd.Flags().Lookup("prefix"))

			var names []string
			cmd.Flags().VisitAll(func(flag *pflag.Flag) {
				names = append(names, flag.Name)
			})
			require.Equal(t, []string{internal.EnvironmentNameFlagName, "force"}, names)
			require.Equal(t, "e", cmd.Flags().Lookup(internal.EnvironmentNameFlagName).Shorthand)
		})
	}
}

func TestEnvUnsetAction(t *testing.T) {
	t.Parallel()

	secretRef := "akvs://sub-id/vault-name/secret-name" //nolint:gosec // G101: test fixture, not a credential
	tests := []struct {
		name   string
		values map[string]string
		args   []string
		want   map[string]string
		noSave bool
	}{
		{
			name:   "SingleKey",
			values: map[string]string{"KEY1": "value1", "KEY2": "value2"},
			args:   []string{"KEY1"},
			want:   map[string]string{"KEY2": "value2"},
		},
		{
			name:   "MultipleKeys",
			values: map[string]string{"KEY1": "value1", "KEY2": "value2", "KEEP": "unchanged"},
			args:   []string{"KEY1", "KEY2"},
			want:   map[string]string{"KEEP": "unchanged"},
		},
		{
			name:   "MissingKey",
			values: map[string]string{"KEEP": "unchanged"},
			args:   []string{"MISSING"},
			want:   map[string]string{"KEEP": "unchanged"},
			noSave: true,
		},
		{
			name:   "RepeatedAndMissingKeys",
			values: map[string]string{"KEY": "value", "KEEP": "unchanged"},
			args:   []string{"KEY", "KEY", "MISSING"},
			want:   map[string]string{"KEEP": "unchanged"},
		},
		{
			name:   "CaseSensitive",
			values: map[string]string{"MY_KEY": "upper", "my_key": "lower"},
			args:   []string{"MY_KEY"},
			want:   map[string]string{"my_key": "lower"},
		},
		{
			name: "ManagedKeyDifferentCase",
			values: map[string]string{
				environment.EnvNameEnvVarName: "test",
				"azure_env_name":              "ordinary-value",
			},
			args: []string{"azure_env_name"},
			want: map[string]string{environment.EnvNameEnvVarName: "test"},
		},
		{
			name:   "SecretReference",
			values: map[string]string{"SECRET": secretRef, "KEEP": "unchanged"},
			args:   []string{"SECRET"},
			want:   map[string]string{"KEEP": "unchanged"},
		},
		{
			name:   "ReservedLoaderKey",
			values: map[string]string{"LD_TEST_UNSET": "filtered-value", "KEEP": "unchanged"},
			args:   []string{"LD_TEST_UNSET"},
			want:   map[string]string{"KEEP": "unchanged"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := environment.NewWithValues("test", maps.Clone(tt.values))
			manager := newTestEnvManager()
			manager.On("Reload", t.Context(), env).Return(nil).Once()
			if !tt.noSave {
				var keys []string
				for _, key := range tt.args {
					if _, exists := tt.values[key]; exists && !slices.Contains(keys, key) {
						keys = append(keys, key)
					}
				}
				manager.On("SaveWithOptions", t.Context(), env, &environment.SaveOptions{DotenvKeys: keys}).
					Return(nil).Once()
			}
			console := mockinput.NewMockConsole()

			result, err := newEnvUnsetAction(
				lazy.From(env), lazy.From[environment.Manager](manager), console, &envUnsetFlags{force: true},
				tt.args).Run(t.Context())
			require.NoError(t, err)
			require.Nil(t, result)
			require.Equal(t, tt.want, env.Dotenv())
			manager.AssertExpectations(t)
			if tt.noSave {
				manager.AssertNotCalled(t, "SaveWithOptions", mock.Anything, mock.Anything, mock.Anything)
			} else {
				manager.AssertNumberOfCalls(t, "SaveWithOptions", 1)
			}
			if slices.Contains(tt.args, "MISSING") {
				require.Len(t, console.Output(), 1)
				require.Contains(t, console.Output()[0], `Environment value "MISSING"`)
				require.Contains(t, console.Output()[0], `"test"`)
				require.Contains(t, console.Output()[0], "ignored")
			} else {
				require.Empty(t, console.Output())
			}
		})
	}
}

func TestEnvUnsetActionRejectsManagedKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "Single", args: []string{environment.EnvNameEnvVarName}},
		{name: "First", args: []string{environment.EnvNameEnvVarName, "KEY"}},
		{name: "Last", args: []string{"KEY", environment.EnvNameEnvVarName}},
		{name: "Repeated", args: []string{"KEY", environment.EnvNameEnvVarName, environment.EnvNameEnvVarName}},
		{name: "AfterMissingKey", args: []string{"MISSING", environment.EnvNameEnvVarName}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			for _, mode := range []struct {
				name  string
				force bool
			}{
				{name: "Interactive"},
				{name: "Force", force: true},
			} {
				t.Run(mode.name, func(t *testing.T) {
					t.Parallel()
					lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
						t.Fatal("invalid keys must be rejected before loading an environment")
						return nil, nil
					})
					manager := newTestEnvManager()
					console := mockinput.NewMockConsole()

					result, err := newEnvUnsetAction(
						lazyEnv, lazy.From[environment.Manager](manager), console, &envUnsetFlags{force: mode.force},
						tt.args).Run(t.Context())
					require.Nil(t, result)
					require.ErrorContains(t, err,
						"cannot unset AZURE_ENV_NAME: this command removes .env keys, not the environment name")
					require.Empty(t, manager.Calls)
					require.Empty(t, console.Output())
				})
			}
		})
	}
}

func TestEnvUnsetActionLoadingErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		loadErr   error
		reloadErr error
	}{
		{name: "MissingEnvironment", loadErr: environment.ErrNotFound},
		{name: "NoEnvironmentSelected", loadErr: environment.ErrNameNotSpecified},
		{name: "LoadingFailure", loadErr: errors.New("load failed")},
		{name: "ReloadingFailure", reloadErr: errors.New("reload failed")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := environment.NewWithValues("test", map[string]string{"KEY": "value"})
			manager := newTestEnvManager()
			lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
				return env, tt.loadErr
			})
			if tt.loadErr == nil {
				manager.On("Reload", t.Context(), env).Return(tt.reloadErr).Once()
			}

			result, err := newEnvUnsetAction(
				lazyEnv, lazy.From[environment.Manager](manager), mockinput.NewMockConsole(), &envUnsetFlags{force: true},
				[]string{"KEY"}).Run(t.Context())
			require.Nil(t, result)
			if tt.loadErr != nil {
				require.ErrorIs(t, err, tt.loadErr)
				require.ErrorContains(t, err, "loading environment")
				require.Equal(t, map[string]string{"KEY": "value"}, env.Dotenv())
			} else {
				require.ErrorIs(t, err, tt.reloadErr)
				require.ErrorContains(t, err, "reloading environment before removal")
			}
			manager.AssertNotCalled(t, "SaveWithOptions", mock.Anything, mock.Anything, mock.Anything)
			require.Equal(t, map[string]string{"KEY": "value"}, env.Dotenv())
			manager.AssertExpectations(t)
		})
	}
}

func TestEnvUnsetActionPersistsDeletion(t *testing.T) {
	t.Setenv("MY_KEY", "process-value")
	secretRef := "akvs://sub-id/vault-name/secret-name" //nolint:gosec // G101: test fixture, not a credential
	_, manager, _ := setupTestEnvironment(t, "test-env", map[string]any{
		"MY_KEY": "config-value",
		"app":    map[string]any{"enabled": true},
	})
	env, err := manager.Get(t.Context(), "test-env")
	require.NoError(t, err)
	for key, value := range map[string]string{
		environment.EnvNameEnvVarName: "test-env",
		"MY_KEY":                      "dotenv-value",
		"my_key":                      "lower-case-value",
		"KEEP":                        "unchanged",
		"EMPTY":                       "",
		"SECRET":                      secretRef,
		"LD_TEST_UNSET":               "filtered-value",
	} {
		env.DotenvSet(key, value)
	}
	require.NoError(t, manager.Save(t.Context(), env))
	envPath, err := manager.EnvPath(env)
	require.NoError(t, err)
	configPath, err := manager.ConfigPath(env)
	require.NoError(t, err)
	configBefore, err := os.ReadFile(configPath)
	require.NoError(t, err)

	diskValues, err := godotenv.Read(envPath)
	require.NoError(t, err)
	require.Contains(t, diskValues, "LD_TEST_UNSET")
	require.NotContains(t, env.Dotenv(), "LD_TEST_UNSET")
	require.NotContains(t, env.Dotenv(), "LATE_REMOVE")

	// Simulate another writer adding values after this environment was loaded.
	diskValues["LATE_REMOVE"] = "remove-me"
	diskValues["LATE_KEEP"] = "keep-me"
	require.NoError(t, godotenv.Write(diskValues, envPath))

	action := newEnvUnsetAction(
		lazy.From(env), lazy.From[environment.Manager](manager), mockinput.NewMockConsole(), &envUnsetFlags{force: true},
		[]string{"MY_KEY", "MY_KEY", "EMPTY", "SECRET", "LD_TEST_UNSET", "LATE_REMOVE", "MISSING"})
	for range 2 {
		result, err := action.Run(t.Context())
		require.NoError(t, err)
		require.Nil(t, result)

		persisted, err := godotenv.Read(envPath)
		require.NoError(t, err)
		require.Equal(t, map[string]string{
			environment.EnvNameEnvVarName: "test-env",
			"my_key":                      "lower-case-value",
			"KEEP":                        "unchanged",
			"LATE_KEEP":                   "keep-me",
		}, persisted)
		configAfter, err := os.ReadFile(configPath)
		require.NoError(t, err)
		require.JSONEq(t, string(configBefore), string(configAfter))
		require.Equal(t, "process-value", os.Getenv("MY_KEY"))
		require.Equal(t, "process-value", env.Getenv("MY_KEY"))
	}
}

func TestEnvUnsetActionPreservesConcurrentWrites(t *testing.T) {
	t.Parallel()
	_, backing, _ := setupTestEnvironment(t, "test-env", map[string]any{"app": map[string]any{"enabled": true}})
	env, err := backing.Get(t.Context(), "test-env")
	require.NoError(t, err)
	env.DotenvSet("KEY", "remove-me")
	env.DotenvSet("KEEP", "stale-value")
	env.DotenvSet("OTHER_REMOVED", "stale-value")
	require.NoError(t, backing.Save(t.Context(), env))
	envPath, err := backing.EnvPath(env)
	require.NoError(t, err)
	configPath, err := backing.ConfigPath(env)
	require.NoError(t, err)

	manager := newTestEnvManager()
	manager.On("Reload", t.Context(), env).Run(func(mock.Arguments) {
		require.NoError(t, backing.Reload(t.Context(), env))
	}).Return(nil).Once()
	options := &environment.SaveOptions{DotenvKeys: []string{"KEY"}}
	manager.On("SaveWithOptions", t.Context(), env, options).Run(func(mock.Arguments) {
		values, err := godotenv.Read(envPath)
		require.NoError(t, err)
		values["KEEP"] = "concurrent-value"
		values["ADDED"] = "concurrent-addition"
		delete(values, "OTHER_REMOVED")
		require.NoError(t, godotenv.Write(values, envPath))
		require.NoError(t, os.WriteFile(configPath, []byte(`{"app":{"enabled":false}}`), 0600))
		require.NoError(t, backing.SaveWithOptions(t.Context(), env, options))
	}).Return(nil).Once()

	result, err := newEnvUnsetAction(
		lazy.From(env), lazy.From[environment.Manager](manager), mockinput.NewMockConsole(), &envUnsetFlags{force: true},
		[]string{"KEY"}).Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)
	values, err := godotenv.Read(envPath)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		environment.EnvNameEnvVarName: "test-env",
		"KEEP":                        "concurrent-value",
		"ADDED":                       "concurrent-addition",
	}, values)
	cfg, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.JSONEq(t, `{"app":{"enabled":false}}`, string(cfg))
	manager.AssertExpectations(t)
}

func TestEnvUnsetActionConfirmation(t *testing.T) {
	t.Parallel()
	promptErr := errors.New("prompt failed")
	tests := []struct {
		name         string
		args         []string
		confirmed    bool
		noPrompt     bool
		force        bool
		promptErr    error
		errorText    string
		errorIs      error
		disappeared  bool
		wantPrompt   bool
		wantModified bool
	}{
		{name: "SingleConfirmed", args: []string{"KEY"}, confirmed: true, wantPrompt: true, wantModified: true},
		{
			name: "MultipleConfirmed", args: []string{"KEY", "KEY2", "KEY", "MISSING"},
			confirmed: true, wantPrompt: true, wantModified: true,
		},
		{name: "Declined", args: []string{"KEY"}, wantPrompt: true},
		{
			name: "PromptFailure", args: []string{"KEY"}, promptErr: promptErr,
			wantPrompt: true, errorText: "confirming environment value removal",
		},
		{
			name: "ClosedStdin", args: []string{"KEY", "KEY2"}, promptErr: io.EOF,
			wantPrompt: true, errorText: "input ended before removal was confirmed; no keys were removed",
		},
		{name: "ForceSkipsPrompt", args: []string{"KEY"}, force: true, wantModified: true},
		{
			name: "NoPromptRequiresForce", args: []string{"KEY"}, noPrompt: true,
			errorText: "requires confirmation", errorIs: internal.ErrInteractiveRequired,
		},
		{name: "NoPromptWithForce", args: []string{"KEY"}, noPrompt: true, force: true, wantModified: true},
		{
			name: "DisappearedDuringConfirmation", args: []string{"KEY"},
			confirmed: true, disappeared: true, wantPrompt: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			before := map[string]string{"KEY": "private-value-one", "KEY2": "private-value-two", "KEEP": "unchanged"}
			env := environment.NewWithValues("dev", maps.Clone(before))
			manager := newTestEnvManager()
			reloads := 1
			if tt.confirmed && tt.promptErr == nil {
				reloads++
			}
			manager.On("Reload", t.Context(), env).Return(nil).Times(reloads)
			if tt.wantModified {
				manager.On("SaveWithOptions", t.Context(), env, mock.Anything).Return(nil).Once()
			}
			console := mockinput.NewMockConsole()
			console.SetNoPromptMode(tt.noPrompt)
			if tt.wantPrompt {
				console.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).
					RespondFn(func(options input.ConsoleOptions) (any, error) {
						require.Equal(t, true, options.DefaultValue)
						require.True(t, options.RequireInput)
						require.Contains(t, options.Message, `environment "dev"`)
						require.Contains(t, options.Message, `"KEY"`)
						require.Equal(t, 1, strings.Count(options.Message, `"KEY"`))
						require.NotContains(t, options.Message, "MISSING")
						require.NotContains(t, options.Message, before["KEY"])
						require.NotContains(t, options.Message, before["KEY2"])
						if len(tt.args) == 1 {
							require.Contains(t, options.Message, "Environment value ")
						} else {
							require.Contains(t, options.Message, "Environment values ")
							require.Contains(t, options.Message, `"KEY2"`)
						}
						if tt.disappeared {
							env.DotenvDelete("KEY")
						}
						return tt.confirmed, tt.promptErr
					})
			}

			result, err := newEnvUnsetAction(
				lazy.From(env), lazy.From[environment.Manager](manager), console, &envUnsetFlags{force: tt.force},
				tt.args).Run(t.Context())
			require.Nil(t, result)
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
				if tt.promptErr != nil {
					require.ErrorIs(t, err, tt.promptErr)
					if errors.Is(tt.promptErr, io.EOF) {
						suggestionErr, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
						require.True(t, ok)
						require.Empty(t, suggestionErr.Message)
						require.Contains(t, suggestionErr.Suggestion, "--force")
					}
				}
				if tt.errorIs != nil {
					require.ErrorIs(t, err, tt.errorIs)
				}
			} else {
				require.NoError(t, err)
			}
			want := maps.Clone(before)
			if tt.wantModified {
				for _, key := range tt.args {
					delete(want, key)
				}
			}
			if tt.disappeared {
				delete(want, "KEY")
				require.Contains(t, strings.Join(console.Output(), "\n"), "was ignored")
			}
			require.Equal(t, want, env.Dotenv())
			if !tt.wantModified {
				manager.AssertNotCalled(t, "SaveWithOptions", mock.Anything, mock.Anything, mock.Anything)
			}
			manager.AssertExpectations(t)
		})
	}
}

func TestEnvUnsetActionProcessOnlyKey(t *testing.T) {
	t.Setenv("PROCESS_ONLY", "process-value")
	env := environment.NewWithValues("dev", map[string]string{"KEEP": "unchanged"})
	manager := newTestEnvManager()
	manager.On("Reload", t.Context(), env).Return(nil).Once()
	console := mockinput.NewMockConsole()
	console.SetNoPromptMode(true)
	result, err := newEnvUnsetAction(
		lazy.From(env), lazy.From[environment.Manager](manager), console, &envUnsetFlags{},
		[]string{"PROCESS_ONLY", "PROCESS_ONLY"}).Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)
	require.Len(t, console.Output(), 1)
	require.Contains(t, console.Output()[0], "was ignored")
	require.Equal(t, "process-value", os.Getenv("PROCESS_ONLY"))
	manager.AssertNotCalled(t, "SaveWithOptions", mock.Anything, mock.Anything, mock.Anything)
	manager.AssertExpectations(t)
}

func TestEnvUnsetActionSaveFailure(t *testing.T) {
	t.Parallel()
	saveErr := errors.New("remote save failed")
	inspectionErr := errors.New("inspection failed")
	restoreReloadErr := errors.New("restore reload failed")
	restoreSaveErr := errors.New("restore save failed")
	promptErr := errors.New("restore prompt failed")
	tests := []struct {
		name                    string
		persistedBeforeError    bool
		restore                 bool
		restoreInput            *string
		force                   bool
		noPrompt                bool
		inspectionErr           error
		restoreReloadErr        error
		restoreSaveErr          error
		promptErr               error
		concurrentUpdate        bool
		updateBeforeRestoreSave bool
		updateDuringConfirm     bool
		cancelDuringSave        bool
		errorText               string
	}{
		{name: "NoPersistedChange", force: true, errorText: "selected local .env values were not modified"},
		{
			name: "NoPersistedChangeWithoutPrompts", force: true, noPrompt: true,
			errorText: "selected local .env values were not modified",
		},
		{
			name: "PartialSaveDeclined", persistedBeforeError: true,
			errorText: "local .env values changed and were not restored",
		},
		{
			name: "PartialSaveRestored", persistedBeforeError: true, restore: true,
			errorText: "previous local .env values were restored",
		},
		{
			name: "PartialSaveEnterRestored", persistedBeforeError: true, restore: true, restoreInput: new("\n"),
			errorText: "previous local .env values were restored",
		},
		{
			name: "PartialSaveWindowsEnterRestored", persistedBeforeError: true, restore: true, restoreInput: new("\r\n"),
			errorText: "previous local .env values were restored",
		},
		{
			name: "PartialSaveExplicitNo", persistedBeforeError: true, restoreInput: new("n\n"),
			errorText: "local .env values changed and were not restored",
		},
		{
			name: "PartialSaveEOFNotRestored", persistedBeforeError: true, restoreInput: new(""), promptErr: io.EOF,
			errorText: "confirming restoration",
		},
		{
			name: "PartialSaveWhitespaceEOFNotRestored", persistedBeforeError: true,
			restoreInput: new(" \t\r"), promptErr: io.EOF, errorText: "confirming restoration",
		},
		{
			name: "PartialSaveInvalidEOFNotRestored", persistedBeforeError: true,
			restoreInput: new("invalid"), promptErr: io.EOF, errorText: "confirming restoration",
		},
		{
			name: "PartialSaveInvalidLineEOFNotRestored", persistedBeforeError: true,
			restoreInput: new("invalid\n"), promptErr: io.EOF, errorText: "confirming restoration",
		},
		{
			name: "PreservesConcurrentUpdates", persistedBeforeError: true, restore: true, concurrentUpdate: true,
			errorText: "previous local .env values were restored",
		},
		{
			name: "PreservesConcurrentUpdatesDuringRestoreSave", persistedBeforeError: true, restore: true,
			updateBeforeRestoreSave: true, errorText: "previous local .env values were restored",
		},
		{
			name: "SnapshotsValuesAfterConfirmation", persistedBeforeError: true, restore: true, updateDuringConfirm: true,
			errorText: "previous local .env values were restored",
		},
		{
			name: "ForcedRestoration", persistedBeforeError: true, force: true,
			errorText: "previous local .env values were restored",
		},
		{
			name: "ForcedRestorationWithoutPrompts", persistedBeforeError: true, force: true, noPrompt: true,
			errorText: "previous local .env values were restored",
		},
		{
			name: "RestoresAfterCancellation", persistedBeforeError: true, force: true,
			noPrompt: true, cancelDuringSave: true, errorText: "previous local .env values were restored",
		},
		{
			name: "RestoresAfterCancellationWithoutForce", persistedBeforeError: true, restore: true,
			cancelDuringSave: true, errorText: "previous local .env values were restored",
		},
		{
			name: "RestoresOnEnterAfterCancellation", persistedBeforeError: true, restore: true, restoreInput: new("\n"),
			cancelDuringSave: true, errorText: "previous local .env values were restored",
		},
		{
			name: "EOFRecoveryAfterCancellation", persistedBeforeError: true, restoreInput: new(""), promptErr: io.EOF,
			cancelDuringSave: true, errorText: "confirming restoration",
		},
		{
			name: "DeclinesRestorationAfterCancellation", persistedBeforeError: true,
			cancelDuringSave: true, errorText: "local .env values changed and were not restored",
		},
		{
			name: "RestorePromptFailureAfterCancellation", persistedBeforeError: true, promptErr: promptErr,
			cancelDuringSave: true, errorText: "confirming restoration",
		},
		{
			name: "InspectionFailure", persistedBeforeError: true, force: true, inspectionErr: inspectionErr,
			errorText: "could not determine whether local .env values changed",
		},
		{
			name: "RestorePromptFailure", persistedBeforeError: true, promptErr: promptErr,
			errorText: "confirming restoration",
		},
		{
			name: "ReloadBeforeRestoreFailure", persistedBeforeError: true, restore: true,
			restoreReloadErr: restoreReloadErr, errorText: "reloading before restoration",
		},
		{
			name: "RestorationSaveFailure", persistedBeforeError: true, restore: true,
			restoreSaveErr: restoreSaveErr, errorText: "restoring previous environment values failed",
		},
		{
			name: "ForcedReloadBeforeRestoreFailure", persistedBeforeError: true, force: true, noPrompt: true,
			restoreReloadErr: restoreReloadErr, errorText: "reloading before restoration",
		},
		{
			name: "ForcedRestorationSaveFailure", persistedBeforeError: true, force: true, noPrompt: true,
			restoreSaveErr: restoreSaveErr, errorText: "restoring previous environment values failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, backing, _ := setupTestEnvironment(t, "test-env", map[string]any{"app": map[string]any{"enabled": true}})
			env, err := backing.Get(t.Context(), "test-env")
			require.NoError(t, err)
			env.DotenvSet("KEY", "original-private-value")
			env.DotenvSet("EMPTY", "")
			env.DotenvSet("LD_TEST_UNSET", "filtered-private-value")
			env.DotenvSet("KEEP", "unchanged")
			env.DotenvSet("OTHER_REMOVED", "unchanged")
			require.NoError(t, backing.Save(t.Context(), env))
			envPath, err := backing.EnvPath(env)
			require.NoError(t, err)
			configPath, err := backing.ConfigPath(env)
			require.NoError(t, err)
			before, err := godotenv.Read(envPath)
			require.NoError(t, err)
			configBefore, err := os.ReadFile(configPath)
			require.NoError(t, err)
			updateUnrelated := func() {
				updated, err := godotenv.Read(envPath)
				require.NoError(t, err)
				updated["KEEP"] = "concurrent-value"
				updated["ADDED"] = "concurrent-addition"
				delete(updated, "OTHER_REMOVED")
				require.NoError(t, godotenv.Write(updated, envPath))
				require.NoError(t, os.WriteFile(configPath, []byte(`{"app":{"enabled":false}}`), 0600))
			}

			runCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			manager := newTestEnvManager()
			reload := func(call mock.Arguments) {
				ctx, ok := call.Get(0).(context.Context)
				require.True(t, ok)
				require.NoError(t, ctx.Err())
				require.NoError(t, backing.Reload(ctx, env))
			}
			manager.On("Reload", mock.Anything, env).Run(reload).Return(nil).Once()
			if !tt.force {
				manager.On("Reload", mock.Anything, env).Run(reload).Return(nil).Once()
			}
			initialErr := saveErr
			if tt.cancelDuringSave {
				initialErr = context.Canceled
			}
			options := &environment.SaveOptions{DotenvKeys: []string{"KEY", "EMPTY", "LD_TEST_UNSET"}}
			manager.On("SaveWithOptions", mock.Anything, env, options).Run(func(call mock.Arguments) {
				if tt.persistedBeforeError {
					ctx, ok := call.Get(0).(context.Context)
					require.True(t, ok)
					require.NoError(t, backing.SaveWithOptions(ctx, env, options))
				}
				if tt.cancelDuringSave {
					cancel()
				}
			}).Return(initialErr).Once()
			if tt.inspectionErr != nil {
				manager.On("Reload", mock.Anything, env).Return(tt.inspectionErr).Once()
			} else {
				manager.On("Reload", mock.Anything, env).Run(reload).Return(nil).Once()
			}
			willRestore := tt.persistedBeforeError && tt.inspectionErr == nil &&
				(tt.force || (tt.restore && tt.promptErr == nil && !tt.noPrompt))
			if willRestore {
				if tt.restoreReloadErr != nil {
					manager.On("Reload", mock.Anything, env).Return(tt.restoreReloadErr).Once()
				} else {
					manager.On("Reload", mock.Anything, env).Run(reload).Return(nil).Once()
					manager.On("SaveWithOptions", mock.Anything, env, options).Run(func(call mock.Arguments) {
						if tt.restoreSaveErr == nil {
							if tt.updateBeforeRestoreSave {
								updateUnrelated()
							}
							ctx, ok := call.Get(0).(context.Context)
							require.True(t, ok)
							require.NoError(t, ctx.Err())
							require.NoError(t, backing.SaveWithOptions(ctx, env, options))
						}
					}).Return(tt.restoreSaveErr).Once()
				}
			}
			recoveryOutput := &bytes.Buffer{}
			console := mockinput.NewMockConsole()
			console.SetNoPromptMode(tt.noPrompt)
			if !tt.force {
				console.WhenConfirm(func(options input.ConsoleOptions) bool {
					return strings.Contains(options.Message, "will be removed from environment")
				}).RespondFn(func(options input.ConsoleOptions) (any, error) {
					require.Equal(t, true, options.DefaultValue)
					require.True(t, options.RequireInput)
					if tt.updateDuringConfirm {
						updated := maps.Clone(before)
						updated["KEY"] = "fresh-private-value"
						updated["KEEP"] = "concurrent-value"
						require.NoError(t, godotenv.Write(updated, envPath))
					}
					return true, nil
				})
			}
			if tt.persistedBeforeError && tt.inspectionErr == nil && !tt.force && !tt.noPrompt {
				console.WhenConfirm(func(options input.ConsoleOptions) bool {
					return strings.Contains(options.Message, "Restore the previous values")
				}).RespondFn(func(options input.ConsoleOptions) (any, error) {
					require.Equal(t, true, options.DefaultValue)
					require.True(t, options.RequireInput)
					require.Contains(t, options.Message, `"test-env"`)
					require.Contains(t, options.Message, `"KEY"`)
					if tt.concurrentUpdate {
						updateUnrelated()
					}
					if tt.restoreInput != nil {
						recoveryConsole := input.NewConsole(false, false, input.Writers{Output: recoveryOutput},
							input.ConsoleHandles{
								Stdin:  strings.NewReader(*tt.restoreInput),
								Stdout: recoveryOutput,
								Stderr: recoveryOutput,
							}, &output.NoneFormatter{}, nil)
						confirmed, err := recoveryConsole.Confirm(context.WithoutCancel(runCtx), options)
						require.Equal(t, tt.restore, confirmed)
						if tt.promptErr != nil {
							require.ErrorIs(t, err, tt.promptErr)
						} else {
							require.NoError(t, err)
						}
						require.Contains(t, recoveryOutput.String(), " (Y/n)")
						return confirmed, err
					}
					return tt.restore, tt.promptErr
				})
			}

			result, err := newEnvUnsetAction(
				lazy.From(env), lazy.From[environment.Manager](manager), envUnsetContextAwareConsole{Console: console},
				&envUnsetFlags{force: tt.force},
				[]string{"KEY", "EMPTY", "LD_TEST_UNSET"}).Run(runCtx)
			require.Nil(t, result)
			require.ErrorIs(t, err, initialErr)
			require.ErrorContains(t, err, tt.errorText)
			for _, additionalErr := range []error{tt.inspectionErr, tt.restoreReloadErr, tt.restoreSaveErr, tt.promptErr} {
				if additionalErr != nil {
					require.ErrorIs(t, err, additionalErr)
				}
			}
			want := maps.Clone(before)
			restored := willRestore && tt.restoreReloadErr == nil && tt.restoreSaveErr == nil
			if tt.persistedBeforeError && !restored {
				delete(want, "KEY")
				delete(want, "EMPTY")
				delete(want, "LD_TEST_UNSET")
			}
			if tt.concurrentUpdate || tt.updateBeforeRestoreSave {
				want["KEEP"] = "concurrent-value"
				want["ADDED"] = "concurrent-addition"
				delete(want, "OTHER_REMOVED")
			}
			if tt.updateDuringConfirm {
				want["KEEP"] = "concurrent-value"
				if restored {
					want["KEY"] = "fresh-private-value"
				}
			}
			persisted, err := godotenv.Read(envPath)
			require.NoError(t, err)
			require.Equal(t, want, persisted)
			configAfter, err := os.ReadFile(configPath)
			require.NoError(t, err)
			if tt.concurrentUpdate || tt.updateBeforeRestoreSave {
				require.JSONEq(t, `{"app":{"enabled":false}}`, string(configAfter))
			} else {
				require.JSONEq(t, string(configBefore), string(configAfter))
			}
			messages := strings.Join(console.Output(), "\n") + recoveryOutput.String()
			if tt.force {
				require.NotContains(t, messages, "will be removed from environment")
				require.NotContains(t, messages, "Restore the previous values")
			}
			if tt.persistedBeforeError && tt.inspectionErr == nil && !tt.force && !tt.noPrompt {
				require.Contains(t, messages, "Restore the previous values")
			}
			require.NotContains(t, messages, "original-private-value")
			require.NotContains(t, messages, "fresh-private-value")
			require.NotContains(t, messages, "filtered-private-value")
			manager.AssertExpectations(t)
		})
	}
}

func TestEnvUnsetActionTelemetry(t *testing.T) {
	loadErr := errors.New("environment loading failed")
	saveErr := errors.New("saving failed before a local write")
	tests := []struct {
		name        string
		args        []string
		force       bool
		confirmed   bool
		disappeared bool
		noPrompt    bool
		loadErr     error
		saveErr     error
		promptErr   error
		outcome     string
		removed     int
		missing     int
	}{
		{
			name: "Success", args: []string{"PRIVATE_KEY", "PRIVATE_KEY", "MISSING", "MISSING"},
			force: true, outcome: "success", removed: 1, missing: 1,
		},
		{
			name: "MissingOnly", args: []string{"MISSING", "MISSING"},
			noPrompt: true, outcome: "no_change", missing: 1,
		},
		{
			name: "UserDeclined", args: []string{"PRIVATE_KEY", "MISSING"},
			outcome: "user_declined", missing: 1,
		},
		{
			name: "Confirmed", args: []string{"PRIVATE_KEY", "MISSING"},
			confirmed: true, outcome: "success", removed: 1, missing: 1,
		},
		{
			name: "DisappearedDuringConfirmation", args: []string{"PRIVATE_KEY", "MISSING"},
			confirmed: true, disappeared: true, outcome: "no_change", missing: 2,
		},
		{
			name: "ConfirmationRequired", args: []string{"PRIVATE_KEY", "MISSING"},
			noPrompt: true, outcome: "failed", missing: 1,
		},
		{
			name: "ClosedStdin", args: []string{"PRIVATE_KEY", "MISSING"},
			promptErr: io.EOF, outcome: "failed", missing: 1,
		},
		{name: "NoKeys", outcome: "failed"},
		{name: "EmptyKey", args: []string{"PRIVATE_KEY", ""}, outcome: "failed"},
		{name: "ProtectedKey", args: []string{"PRIVATE_KEY", environment.EnvNameEnvVarName}, outcome: "failed"},
		{name: "LoadingFailure", args: []string{"PRIVATE_KEY"}, loadErr: loadErr, outcome: "failed"},
		{
			name: "SaveFailure", args: []string{"PRIVATE_KEY", "MISSING"},
			force: true, saveErr: saveErr, outcome: "failed", missing: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			provider := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
			defer func() {
				require.NoError(t, provider.Shutdown(context.Background()))
			}()
			ctx, span := provider.Tracer("test").Start(t.Context(), "cmd.env.unset")
			defer span.End()
			env := environment.NewWithValues("private-environment", map[string]string{
				"PRIVATE_KEY": "private-stored-value",
				"KEEP":        "unchanged",
			})
			manager := newTestEnvManager()
			valid := validateEnvUnsetKeys(tt.args) == nil
			lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
				require.True(t, valid, "invalid arguments must not load an environment")
				return env, tt.loadErr
			})
			lazyManager := lazy.NewLazy(func() (environment.Manager, error) {
				require.True(t, valid, "invalid arguments must not resolve the environment manager")
				return manager, nil
			})
			console := mockinput.NewMockConsole()
			console.SetNoPromptMode(tt.noPrompt)
			if valid && tt.loadErr == nil {
				reloads := 1
				if !tt.force && tt.confirmed && !tt.noPrompt {
					reloads++
				}
				manager.On("Reload", ctx, env).Return(nil).Times(reloads)
				if !tt.force && !tt.noPrompt && slices.Contains(tt.args, "PRIVATE_KEY") {
					console.WhenConfirm(func(input.ConsoleOptions) bool { return true }).
						RespondFn(func(input.ConsoleOptions) (any, error) {
							if tt.disappeared {
								env.DotenvDelete("PRIVATE_KEY")
							}
							return tt.confirmed, tt.promptErr
						})
				}
				if tt.outcome == "success" || tt.saveErr != nil {
					manager.On("SaveWithOptions", ctx, env,
						&environment.SaveOptions{DotenvKeys: []string{"PRIVATE_KEY"}}).Return(tt.saveErr).Once()
				}
				if tt.saveErr != nil {
					manager.On("Reload", mock.Anything, env).Run(func(mock.Arguments) {
						env.DotenvSet("PRIVATE_KEY", "private-stored-value")
					}).Return(nil).Once()
				}
			}

			_, err := newEnvUnsetAction(lazyEnv, lazyManager, console,
				&envUnsetFlags{force: tt.force}, tt.args).Run(ctx)
			if tt.outcome == "failed" {
				require.Error(t, err)
				if tt.promptErr != nil {
					require.ErrorIs(t, err, tt.promptErr)
					require.Equal(t, "private-stored-value", env.Getenv("PRIVATE_KEY"))
					manager.AssertNotCalled(t, "SaveWithOptions", mock.Anything, mock.Anything, mock.Anything)
				}
			} else {
				require.NoError(t, err)
			}
			span.End()
			ended := recorder.Ended()
			require.Len(t, ended, 1)
			attributes := ended[0].Attributes()
			require.ElementsMatch(t, []attribute.KeyValue{
				fields.EnvUnsetOutcomeKey.String(tt.outcome),
				fields.EnvUnsetRemovedCountKey.Int(tt.removed),
				fields.EnvUnsetMissingCountKey.Int(tt.missing),
			}, attributes)
			for _, attr := range attributes {
				require.NotContains(t, attr.Value.String(), "private-environment")
				require.NotContains(t, attr.Value.String(), "PRIVATE_KEY")
				require.NotContains(t, attr.Value.String(), "private-stored-value")
			}
			for _, attr := range tracing.GetUsageAttributes() {
				require.False(t, strings.HasPrefix(string(attr.Key), "env.unset."),
					"unset attributes must not leak into other command spans")
			}
			manager.AssertExpectations(t)
		})
	}
}

type envUnsetContextAwareConsole struct {
	input.Console
}

func (c envUnsetContextAwareConsole) Confirm(ctx context.Context, options input.ConsoleOptions) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return c.Console.Confirm(ctx, options)
}
