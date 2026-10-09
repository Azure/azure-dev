// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/joho/godotenv"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := newEnvUnsetCmd()
			require.Equal(t, "unset <key...>", cmd.Use)
			require.NotEmpty(t, cmd.Short)
			require.NotEmpty(t, cmd.Example)
			require.Contains(t, getCmdEnvUnsetHelpDescription(cmd), "config.json")
			require.Contains(t, getCmdEnvUnsetHelpDescription(cmd), "does not delete the secret")
			require.Contains(t, cmd.Long, "ignored with a warning")
			require.Contains(t, cmd.Long, "--force")
			require.Contains(t, cmd.Long, "restore the previous values")
			require.Contains(t, cmd.Long, "With --force, restoration is attempted automatically without prompting.")
			require.NotContains(t, cmd.Long, "Process environment variables")
			require.NotContains(t, cmd.Long, "Managed values")

			err := cmd.Args(cmd, tt.args)
			if tt.errorContains != "" {
				require.ErrorContains(t, err, tt.errorContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
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
				manager.On("Save", t.Context(), env).Return(nil).Once()
			}
			console := mockinput.NewMockConsole()

			result, err := newEnvUnsetAction(
				lazy.From(env), manager, console, &envUnsetFlags{force: true}, tt.args).Run(t.Context())
			require.NoError(t, err)
			require.Nil(t, result)
			require.Equal(t, tt.want, env.Dotenv())
			manager.AssertExpectations(t)
			if tt.noSave {
				manager.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
			} else {
				manager.AssertNumberOfCalls(t, "Save", 1)
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
				lazyEnv, manager, mockinput.NewMockConsole(), &envUnsetFlags{force: true}, []string{"KEY"}).Run(t.Context())
			require.Nil(t, result)
			if tt.loadErr != nil {
				require.ErrorIs(t, err, tt.loadErr)
				require.ErrorContains(t, err, "loading environment")
				require.Equal(t, map[string]string{"KEY": "value"}, env.Dotenv())
			} else {
				require.ErrorIs(t, err, tt.reloadErr)
				require.ErrorContains(t, err, "reloading environment before removal")
			}
			manager.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
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
	configBefore, err := os.ReadFile(manager.ConfigPath(env))
	require.NoError(t, err)

	diskValues, err := godotenv.Read(manager.EnvPath(env))
	require.NoError(t, err)
	require.Contains(t, diskValues, "LD_TEST_UNSET")
	require.NotContains(t, env.Dotenv(), "LD_TEST_UNSET")
	require.NotContains(t, env.Dotenv(), "LATE_REMOVE")

	// Simulate another writer adding values after this environment was loaded.
	diskValues["LATE_REMOVE"] = "remove-me"
	diskValues["LATE_KEEP"] = "keep-me"
	require.NoError(t, godotenv.Write(diskValues, manager.EnvPath(env)))

	action := newEnvUnsetAction(
		lazy.From(env), manager, mockinput.NewMockConsole(), &envUnsetFlags{force: true},
		[]string{"MY_KEY", "MY_KEY", "EMPTY", "SECRET", "LD_TEST_UNSET", "LATE_REMOVE", "MISSING"})
	for range 2 {
		result, err := action.Run(t.Context())
		require.NoError(t, err)
		require.Nil(t, result)

		persisted, err := godotenv.Read(manager.EnvPath(env))
		require.NoError(t, err)
		require.Equal(t, map[string]string{
			environment.EnvNameEnvVarName: "test-env",
			"my_key":                      "lower-case-value",
			"KEEP":                        "unchanged",
			"LATE_KEEP":                   "keep-me",
		}, persisted)
		configAfter, err := os.ReadFile(manager.ConfigPath(env))
		require.NoError(t, err)
		require.JSONEq(t, string(configBefore), string(configAfter))
		require.Equal(t, "process-value", os.Getenv("MY_KEY"))
		require.Equal(t, "process-value", env.Getenv("MY_KEY"))
	}
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
		{name: "ForceSkipsPrompt", args: []string{"KEY"}, force: true, wantModified: true},
		{
			name: "NoPromptRequiresForce", args: []string{"KEY"}, noPrompt: true,
			errorText: "requires confirmation",
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
				manager.On("Save", t.Context(), env).Return(nil).Once()
			}
			console := mockinput.NewMockConsole()
			console.SetNoPromptMode(tt.noPrompt)
			if tt.wantPrompt {
				console.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).
					RespondFn(func(options input.ConsoleOptions) (any, error) {
						require.Equal(t, false, options.DefaultValue)
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
				lazy.From(env), manager, console, &envUnsetFlags{force: tt.force}, tt.args).Run(t.Context())
			require.Nil(t, result)
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
				if tt.promptErr != nil {
					require.ErrorIs(t, err, tt.promptErr)
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
				manager.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
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
		lazy.From(env), manager, console, &envUnsetFlags{}, []string{"PROCESS_ONLY", "PROCESS_ONLY"}).Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)
	require.Len(t, console.Output(), 1)
	require.Contains(t, console.Output()[0], "was ignored")
	require.Equal(t, "process-value", os.Getenv("PROCESS_ONLY"))
	manager.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
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
		name                 string
		persistedBeforeError bool
		restore              bool
		force                bool
		noPrompt             bool
		inspectionErr        error
		restoreReloadErr     error
		restoreSaveErr       error
		promptErr            error
		concurrentUpdate     bool
		updateDuringConfirm  bool
		cancelDuringSave     bool
		errorText            string
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
			name: "PreservesConcurrentUpdates", persistedBeforeError: true, restore: true, concurrentUpdate: true,
			errorText: "previous local .env values were restored",
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
			require.NoError(t, backing.Save(t.Context(), env))
			before, err := godotenv.Read(backing.EnvPath(env))
			require.NoError(t, err)
			configBefore, err := os.ReadFile(backing.ConfigPath(env))
			require.NoError(t, err)

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
			manager.On("Save", mock.Anything, env).Run(func(call mock.Arguments) {
				if tt.persistedBeforeError {
					ctx, ok := call.Get(0).(context.Context)
					require.True(t, ok)
					require.NoError(t, backing.Save(ctx, env))
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
					manager.On("Save", mock.Anything, env).Run(func(call mock.Arguments) {
						if tt.restoreSaveErr == nil {
							ctx, ok := call.Get(0).(context.Context)
							require.True(t, ok)
							require.NoError(t, ctx.Err())
							require.NoError(t, backing.Save(ctx, env))
						}
					}).Return(tt.restoreSaveErr).Once()
				}
			}
			console := mockinput.NewMockConsole()
			console.SetNoPromptMode(tt.noPrompt)
			if !tt.force {
				console.WhenConfirm(func(options input.ConsoleOptions) bool {
					return strings.Contains(options.Message, "will be removed from environment")
				}).RespondFn(func(options input.ConsoleOptions) (any, error) {
					if tt.updateDuringConfirm {
						updated := maps.Clone(before)
						updated["KEY"] = "fresh-private-value"
						updated["KEEP"] = "concurrent-value"
						require.NoError(t, godotenv.Write(updated, backing.EnvPath(env)))
					}
					return true, nil
				})
			}
			if tt.persistedBeforeError && tt.inspectionErr == nil && !tt.force && !tt.noPrompt {
				console.WhenConfirm(func(options input.ConsoleOptions) bool {
					return strings.Contains(options.Message, "Restore the previous values")
				}).RespondFn(func(options input.ConsoleOptions) (any, error) {
					require.Equal(t, false, options.DefaultValue)
					require.Contains(t, options.Message, `"test-env"`)
					require.Contains(t, options.Message, `"KEY"`)
					if tt.concurrentUpdate {
						updated, err := godotenv.Read(backing.EnvPath(env))
						require.NoError(t, err)
						updated["KEEP"] = "concurrent-value"
						updated["ADDED"] = "concurrent-addition"
						require.NoError(t, godotenv.Write(updated, backing.EnvPath(env)))
						require.NoError(t, os.WriteFile(
							backing.ConfigPath(env), []byte(`{"app":{"enabled":false}}`), 0600))
					}
					return tt.restore, tt.promptErr
				})
			}

			result, err := newEnvUnsetAction(
				lazy.From(env), manager, console, &envUnsetFlags{force: tt.force},
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
			if tt.concurrentUpdate {
				want["KEEP"] = "concurrent-value"
				want["ADDED"] = "concurrent-addition"
			}
			if tt.updateDuringConfirm {
				want["KEEP"] = "concurrent-value"
				if restored {
					want["KEY"] = "fresh-private-value"
				}
			}
			persisted, err := godotenv.Read(backing.EnvPath(env))
			require.NoError(t, err)
			require.Equal(t, want, persisted)
			configAfter, err := os.ReadFile(backing.ConfigPath(env))
			require.NoError(t, err)
			if tt.concurrentUpdate {
				require.JSONEq(t, `{"app":{"enabled":false}}`, string(configAfter))
			} else {
				require.JSONEq(t, string(configBefore), string(configAfter))
			}
			messages := strings.Join(console.Output(), "\n")
			if tt.force {
				require.NotContains(t, messages, "will be removed from environment")
				require.NotContains(t, messages, "Restore the previous values")
			}
			require.NotContains(t, messages, "original-private-value")
			require.NotContains(t, messages, "fresh-private-value")
			require.NotContains(t, messages, "filtered-private-value")
			manager.AssertExpectations(t)
		})
	}
}
