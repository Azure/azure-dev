// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReservedFlagsRegistryPopulated(t *testing.T) {
	// Sanity check: the registry should contain the known global flags.
	require.GreaterOrEqual(t, len(ReservedFlags), 9, "expected at least 9 reserved flags")
}

func TestIsReservedShortFlag(t *testing.T) {
	tests := []struct {
		short    string
		reserved bool
	}{
		{"e", true},
		{"C", true},
		{"o", true},
		{"h", true},
		// Flags with no short form should NOT appear as short flags.
		{"", false},
		// Arbitrary letters should not be reserved.
		{"x", false},
		{"z", false},
		{"p", false},
	}

	for _, tt := range tests {
		t.Run("short="+tt.short, func(t *testing.T) {
			require.Equal(t, tt.reserved, IsReservedShortFlag(tt.short))
		})
	}
}

func TestIsReservedLongFlag(t *testing.T) {
	tests := []struct {
		long     string
		reserved bool
	}{
		{"environment", true},
		{"cwd", true},
		{"debug", true},
		{"no-prompt", true},
		{"output", true},
		{"help", true},
		{"docs", true},
		{"trace-log-file", true},
		{"trace-log-url", true},
		// Not reserved.
		{"verbose", false},
		{"project-endpoint", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run("long="+tt.long, func(t *testing.T) {
			require.Equal(t, tt.reserved, IsReservedLongFlag(tt.long))
		})
	}
}

func TestGetReservedShortFlag(t *testing.T) {
	f, ok := GetReservedShortFlag("e")
	require.True(t, ok)
	require.Equal(t, "environment", f.Long)
	require.Equal(t, "e", f.Short)

	f, ok = GetReservedShortFlag("C")
	require.True(t, ok)
	require.Equal(t, "cwd", f.Long)

	_, ok = GetReservedShortFlag("x")
	require.False(t, ok)
}

func TestGetReservedLongFlag(t *testing.T) {
	f, ok := GetReservedLongFlag("debug")
	require.True(t, ok)
	require.Equal(t, "debug", f.Long)
	require.Empty(t, f.Short, "debug has no short form")

	_, ok = GetReservedLongFlag("nonexistent")
	require.False(t, ok)
}

func TestReservedFlagsNoDuplicates(t *testing.T) {
	seenLong := make(map[string]bool)
	seenShort := make(map[string]bool)

	for _, f := range ReservedFlags {
		require.NotEmpty(t, f.Long, "every reserved flag must have a long name")
		require.False(t, seenLong[f.Long], "duplicate long flag: %s", f.Long)
		seenLong[f.Long] = true

		if f.Short != "" {
			require.False(t, seenShort[f.Short], "duplicate short flag: %s", f.Short)
			seenShort[f.Short] = true
		}
	}
}

func TestReservedFlagsConsistentWithLookups(t *testing.T) {
	// Every entry in ReservedFlags must be findable via the lookup helpers.
	for _, f := range ReservedFlags {
		require.True(t, IsReservedLongFlag(f.Long), "long flag %q not found via IsReservedLongFlag", f.Long)
		if f.Short != "" {
			require.True(t, IsReservedShortFlag(f.Short), "short flag %q not found via IsReservedShortFlag", f.Short)
		}
	}
}
