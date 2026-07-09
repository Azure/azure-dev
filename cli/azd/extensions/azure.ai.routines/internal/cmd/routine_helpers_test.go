// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"testing"
	"time"

	"azure.ai.routines/internal/exterrors"
	"azure.ai.routines/internal/pkg/routines"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func unsetEnv(t *testing.T, key string) {
	t.Helper()

	old, found := os.LookupEnv(key)
	require.NoError(t, os.Unsetenv(key))
	t.Cleanup(func() {
		if found {
			require.NoError(t, os.Setenv(key, old))
			return
		}
		require.NoError(t, os.Unsetenv(key))
	})
}

// --- HTTP timeout config

func TestRoutineHTTPTimeoutOverrideFromEnv_Default(t *testing.T) {
	unsetEnv(t, routineHTTPTimeoutEnvVar)

	got, err := routineHTTPTimeoutOverrideFromEnv()
	require.NoError(t, err)
	assert.Zero(t, got)
}

func TestRoutineHTTPTimeoutOverrideFromEnv_Override(t *testing.T) {
	t.Setenv(routineHTTPTimeoutEnvVar, "90s")

	got, err := routineHTTPTimeoutOverrideFromEnv()
	require.NoError(t, err)
	assert.Equal(t, 90*time.Second, got)
}

func TestRoutineHTTPTimeoutOverrideFromEnv_Invalid(t *testing.T) {
	t.Setenv(routineHTTPTimeoutEnvVar, "soon")

	_, err := routineHTTPTimeoutOverrideFromEnv()
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
	assert.Contains(t, localErr.Message, routineHTTPTimeoutEnvVar)
}

func TestRoutineHTTPTimeoutOverrideFromCommand_FlagWins(t *testing.T) {
	t.Setenv(routineHTTPTimeoutEnvVar, "5m")
	cmd := &cobra.Command{}
	cmd.Flags().String(routineHTTPTimeoutFlag, "", "")
	require.NoError(t, cmd.Flags().Set(routineHTTPTimeoutFlag, "90s"))

	got, err := routineHTTPTimeoutOverrideFromCommand(cmd)
	require.NoError(t, err)
	assert.Equal(t, 90*time.Second, got)
}

func TestRoutineHTTPTimeoutOverrideFromCommand_InheritedFlagWins(t *testing.T) {
	t.Setenv(routineHTTPTimeoutEnvVar, "5m")
	var got time.Duration

	rootCmd := &cobra.Command{Use: "root"}
	rootCmd.PersistentFlags().String(routineHTTPTimeoutFlag, "", "")
	childCmd := &cobra.Command{
		Use: "child",
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			got, err = routineHTTPTimeoutOverrideFromCommand(cmd)
			return err
		},
	}
	rootCmd.AddCommand(childCmd)
	rootCmd.SetArgs([]string{"--timeout", "90s", "child"})

	require.NoError(t, rootCmd.Execute())
	assert.Equal(t, 90*time.Second, got)
}

func TestRoutineClientOptions_DefaultsToSeparateTimeouts(t *testing.T) {
	assert.Nil(t, routineClientOptions(0))
	assert.Equal(t, &routines.ClientOptions{
		RequestTimeout: 90 * time.Second,
	}, routineClientOptions(90*time.Second))
}

func TestRootCommandRegistersTimeoutFlag(t *testing.T) {
	rootCmd := NewRootCommand()

	flag := rootCmd.PersistentFlags().Lookup(routineHTTPTimeoutFlag)
	require.NotNil(t, flag)
	assert.Empty(t, flag.DefValue)
}

// ─── boolStr ─────────────────────────────────────────────────────────────────

func TestBoolStr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		val  *bool
		want string
	}{
		{"nil", nil, "unknown"},
		{"true", new(true), "true"},
		{"false", new(false), "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, boolStr(tt.val))
		})
	}
}

// ─── sortedKeys ──────────────────────────────────────────────────────────────

func TestSortedKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		m    map[string]int
		want []string
	}{
		{"nil map", nil, nil},
		{"empty map", map[string]int{}, nil},
		{"single", map[string]int{"a": 1}, []string{"a"}},
		{"multiple", map[string]int{"c": 3, "a": 1, "b": 2}, []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sortedKeys(tt.m)
			assert.Equal(t, tt.want, got)
		})
	}
}
