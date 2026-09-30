// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"os"
	"strings"
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

func TestRoutineSummaryTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		authorization *routines.RoutineAuthorization
		wantIdentity  string
	}{
		{
			name: "creator identity",
			authorization: &routines.RoutineAuthorization{
				Identity: routines.RoutineDispatchIdentityCreator,
			},
			wantIdentity: routines.RoutineDispatchIdentityCreator,
		},
		{
			name: "agent identity",
			authorization: &routines.RoutineAuthorization{
				Identity: routines.RoutineDispatchIdentityAgent,
			},
			wantIdentity: routines.RoutineDispatchIdentityAgent,
		},
		{
			name:         "missing authorization",
			wantIdentity: "unknown",
		},
		{
			name:          "empty identity",
			authorization: &routines.RoutineAuthorization{},
			wantIdentity:  "unknown",
		},
		{
			name: "other non-empty identity",
			authorization: &routines.RoutineAuthorization{
				Identity: "other",
			},
			wantIdentity: "other",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			routine := &routines.Routine{
				Name:          "nightly",
				Description:   "Daily summary",
				Enabled:       new(true),
				Authorization: test.authorization,
				Triggers: map[string]routines.RoutineTrigger{
					"default": {
						Type:           "schedule",
						CronExpression: "0 8 * * *",
					},
				},
				Action: &routines.RoutineAction{
					Type:      "invoke_agent_responses_api",
					AgentName: "summarizer",
				},
			}

			require.NoError(t, routineSummaryTable(&output, routine))
			outputText := output.String()
			for _, expected := range []string{
				"Name:",
				"nightly",
				"Description:",
				"Daily summary",
				"Enabled:",
				"Trigger (default):",
				"schedule",
				"Cron:",
				"0 8 * * *",
				"Action:",
				"invoke_agent_responses_api",
				"AgentName:",
				"summarizer",
			} {
				assert.Contains(t, outputText, expected)
			}

			var identity string
			foundIdentity := false
			for _, line := range strings.Split(outputText, "\n") {
				if strings.HasPrefix(line, "Dispatch identity:") {
					identity = strings.TrimSpace(strings.TrimPrefix(
						line,
						"Dispatch identity:",
					))
					foundIdentity = true
					break
				}
			}
			require.True(t, foundIdentity)
			assert.Equal(t, test.wantIdentity, identity)
		})
	}
}

type routineSummaryFailingWriter struct {
	err error
}

func (writer routineSummaryFailingWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

func TestRoutineSummaryTableReturnsWriteError(t *testing.T) {
	t.Parallel()

	writeErr := errors.New("write failed")
	err := routineSummaryTable(
		routineSummaryFailingWriter{err: writeErr},
		&routines.Routine{Name: "nightly"},
	)
	require.ErrorIs(t, err, writeErr)
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
