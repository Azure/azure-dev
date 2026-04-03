// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOnlyConfigFlagsSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		channel  string
		interval int
		expected bool
	}{
		{
			name:     "only_interval_set",
			channel:  "",
			interval: 24,
			expected: true,
		},
		{
			name:     "channel_and_interval",
			channel:  "stable",
			interval: 24,
			expected: false,
		},
		{
			name:     "only_channel_set",
			channel:  "stable",
			interval: 0,
			expected: false,
		},
		{
			name:     "neither_set",
			channel:  "",
			interval: 0,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			action := &updateAction{
				flags: &updateFlags{
					channel:            tt.channel,
					checkIntervalHours: tt.interval,
				},
			}

			result := action.onlyConfigFlagsSet()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPersistNonChannelFlags(t *testing.T) {
	t.Parallel()

	t.Run("no_flags_set", func(t *testing.T) {
		t.Parallel()

		action := &updateAction{
			flags: &updateFlags{
				checkIntervalHours: 0,
			},
		}

		cfg := config.NewEmptyConfig()
		changed, err := action.persistNonChannelFlags(cfg)
		require.NoError(t, err)
		assert.False(t, changed)
	})

	t.Run("interval_set", func(t *testing.T) {
		t.Parallel()

		action := &updateAction{
			flags: &updateFlags{
				checkIntervalHours: 12,
			},
		}

		cfg := config.NewEmptyConfig()
		changed, err := action.persistNonChannelFlags(cfg)
		require.NoError(t, err)
		assert.True(t, changed)

		// Verify the interval was saved
		updateCfg := update.LoadUpdateConfig(cfg)
		assert.Equal(t, 12, updateCfg.CheckIntervalHours)
	})
}

func TestUpdateErrorCodes(t *testing.T) {
	t.Parallel()

	// Verify telemetry result codes used in updateAction.Run() are non-empty
	// and follow the expected "update." prefix convention.
	codes := []string{
		update.CodeSuccess,
		update.CodeAlreadyUpToDate,
		update.CodeVersionCheckFailed,
		update.CodeSkippedCI,
		update.CodePackageManagerFailed,
		update.CodeChannelSwitchDecline,
		update.CodeReplaceFailed,
		update.CodeConfigFailed,
		update.CodeInvalidInput,
	}

	for _, code := range codes {
		assert.NotEmpty(t, code)
		assert.Contains(t, code, "update.")
	}
}
