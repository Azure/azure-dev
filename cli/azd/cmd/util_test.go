// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestParseKeyValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arg       string
		wantKey   string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "simple_key_value",
			arg:       "KEY=VALUE",
			wantKey:   "KEY",
			wantValue: "VALUE",
		},
		{
			name:      "value_with_equals",
			arg:       "KEY=VALUE=WITH=EQUALS",
			wantKey:   "KEY",
			wantValue: "VALUE=WITH=EQUALS",
		},
		{
			name:      "empty_value",
			arg:       "KEY=",
			wantKey:   "KEY",
			wantValue: "",
		},
		{
			name:      "value_with_spaces",
			arg:       "KEY=hello world",
			wantKey:   "KEY",
			wantValue: "hello world",
		},
		{
			name:    "no_equals_sign",
			arg:     "KEYONLY",
			wantErr: true,
		},
		{
			name:    "empty_string",
			arg:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			key, value, err := parseKeyValue(tt.arg)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid key=value format")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantKey, key)
			require.Equal(t, tt.wantValue, value)
		})
	}
}

func TestWarnKeyCaseConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		dotEnv map[string]string
		key    string
	}{
		{
			name:   "no_conflicts",
			dotEnv: map[string]string{"OTHER_KEY": "val"},
			key:    "MY_KEY",
		},
		{
			name:   "exact_match_no_conflict",
			dotEnv: map[string]string{"MY_KEY": "val"},
			key:    "MY_KEY",
		},
		{
			name:   "single_case_conflict",
			dotEnv: map[string]string{"My_Key": "val"},
			key:    "MY_KEY",
		},
		{
			name:   "multiple_case_conflicts",
			dotEnv: map[string]string{"My_Key": "v1", "my_key": "v2"},
			key:    "MY_KEY",
		},
		{
			name:   "empty_dotenv",
			dotEnv: map[string]string{},
			key:    "MY_KEY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockContext := mocks.NewMockContext(context.Background())
			// Verify the function doesn't panic with any input
			warnKeyCaseConflicts(t.Context(), mockContext.Console, tt.dotEnv, tt.key)
		})
	}
}

func TestServiceNameWarningCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		serviceName string
		commandName string
	}{
		{
			name:        "empty_service_name_no_warning",
			serviceName: "",
			commandName: "deploy",
		},
		{
			name:        "non_empty_service_name_shows_warning",
			serviceName: "myservice",
			commandName: "deploy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockContext := mocks.NewMockContext(context.Background())
			// Should not panic regardless of input
			serviceNameWarningCheck(mockContext.Console, tt.serviceName, tt.commandName)
		})
	}
}

func TestCountTrue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		elms     []bool
		expected int
	}{
		{
			name:     "all_false",
			elms:     []bool{false, false, false},
			expected: 0,
		},
		{
			name:     "all_true",
			elms:     []bool{true, true, true},
			expected: 3,
		},
		{
			name:     "mixed",
			elms:     []bool{true, false, true, false},
			expected: 2,
		},
		{
			name:     "single_true",
			elms:     []bool{true},
			expected: 1,
		},
		{
			name:     "single_false",
			elms:     []bool{false},
			expected: 0,
		},
		{
			name:     "empty",
			elms:     []bool{},
			expected: 0,
		},
		{
			name:     "nil_variadic",
			elms:     nil,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := countTrue(tt.elms...)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestWithBrowserOverride(t *testing.T) {
	t.Parallel()

	t.Run("sets_and_retrieves_override", func(t *testing.T) {
		t.Parallel()
		var capturedURL string
		ctx := WithBrowserOverride(context.Background(),
			func(_ context.Context, _ input.Console, url string) {
				capturedURL = url
			})
		require.NotNil(t, ctx)

		// Verify the value is retrievable
		val, ok := ctx.Value(browserOverrideKey{}).(browseUrl)
		require.True(t, ok)
		require.NotNil(t, val)

		// Verify override is callable
		val(ctx, nil, "https://example.com")
		require.Equal(t, "https://example.com", capturedURL)
	})

	t.Run("nil_context_value_without_override", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		val := ctx.Value(browserOverrideKey{})
		require.Nil(t, val)
	})
}
