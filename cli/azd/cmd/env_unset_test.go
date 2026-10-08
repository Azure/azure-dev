// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"maps"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
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
	}{
		{name: "ProcessDefault", environment: "process-env"},
		{name: "LongFlag", args: []string{"--environment", "target-env"}, environment: "target-env"},
		{name: "ShortFlag", args: []string{"-e", "target-env"}, environment: "target-env"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newEnvUnsetCmd()
			global := &internal.GlobalCommandOptions{}
			flags := newEnvUnsetFlags(cmd, global)
			require.NoError(t, cmd.Flags().Parse(tt.args))
			require.Equal(t, tt.environment, flags.EnvironmentName)
			require.Same(t, global, flags.global)

			var names []string
			cmd.Flags().VisitAll(func(flag *pflag.Flag) {
				names = append(names, flag.Name)
			})
			require.Equal(t, []string{internal.EnvironmentNameFlagName}, names)
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := environment.NewWithValues("test", maps.Clone(tt.values))
			manager := newTestEnvManager()
			manager.On("Save", t.Context(), env).Return(nil).Once()

			result, err := newEnvUnsetAction(lazy.From(env), manager, tt.args).Run(t.Context())
			require.NoError(t, err)
			require.Nil(t, result)
			require.Equal(t, tt.want, env.Dotenv())
			manager.AssertExpectations(t)
			manager.AssertNumberOfCalls(t, "Save", 1)
		})
	}
}

func TestEnvUnsetActionErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		loadErr error
		saveErr error
	}{
		{name: "MissingEnvironment", loadErr: environment.ErrNotFound},
		{name: "NoEnvironmentSelected", loadErr: environment.ErrNameNotSpecified},
		{name: "LoadingFailure", loadErr: errors.New("load failed")},
		{name: "SavingFailure", saveErr: errors.New("save failed")},
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
				manager.On("Save", t.Context(), env).Return(tt.saveErr).Once()
			}

			result, err := newEnvUnsetAction(lazyEnv, manager, []string{"KEY"}).Run(t.Context())
			require.Nil(t, result)
			if tt.loadErr != nil {
				require.ErrorIs(t, err, tt.loadErr)
				require.ErrorContains(t, err, "loading environment")
				require.Equal(t, map[string]string{"KEY": "value"}, env.Dotenv())
				manager.AssertNotCalled(t, "Save", mock.Anything, mock.Anything)
			} else {
				require.ErrorIs(t, err, tt.saveErr)
				require.ErrorContains(t, err, "saving environment")
			}
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

	action := newEnvUnsetAction(lazy.From(env), manager, []string{
		"MY_KEY", "MY_KEY", "EMPTY", "SECRET", "LD_TEST_UNSET", "LATE_REMOVE", "MISSING",
	})
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
