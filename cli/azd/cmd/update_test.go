// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubUserConfigManager is a minimal UserConfigManager for testing.
type stubUserConfigManager struct {
	config config.Config
	saved  bool
}

func (m *stubUserConfigManager) Load() (config.Config, error) {
	return m.config, nil
}

func (m *stubUserConfigManager) Save(c config.Config) error {
	m.saved = true
	m.config = c
	return nil
}

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

	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		assert.NotEmpty(t, code)
		assert.True(t, strings.HasPrefix(code, "update."),
			"code %q should have prefix %q", code, "update.")
		assert.False(t, seen[code], "duplicate code %q", code)
		seen[code] = true
	}
}

func TestUpdateAction_BetaNoticeFirstUse(t *testing.T) {
	t.Parallel()

	// Override version to a production-like string so IsNonProdVersion() returns false.
	saved := internal.Version
	internal.Version = "1.0.0 (commit 0000000000000000000000000000000000000000)"
	t.Cleanup(func() { internal.Version = saved })

	t.Run("shows notice and persists channel on empty config", func(t *testing.T) {
		t.Parallel()

		console := mockinput.NewMockConsole()
		cfg := config.NewEmptyConfig()
		mgr := &stubUserConfigManager{config: cfg}

		action := &updateAction{
			flags: &updateFlags{
				channel:            "",
				checkIntervalHours: 12, // triggers onlyConfigFlagsSet() → early return
			},
			console:       console,
			formatter:     &output.NoneFormatter{},
			writer:        &bytes.Buffer{},
			configManager: mgr,
		}

		result, err := action.Run(t.Context())
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify beta notice was displayed
		messages := console.Output()
		require.True(t, len(messages) > 0, "expected at least one console message")
		assert.Contains(t, messages[0], "Beta")

		// Verify config was saved (default channel persisted + interval)
		assert.True(t, mgr.saved, "config should have been saved")
		assert.True(t, update.HasUpdateConfig(cfg), "config should have update keys after first use")
	})

	t.Run("skips notice when config already exists", func(t *testing.T) {
		t.Parallel()

		console := mockinput.NewMockConsole()
		cfg := config.NewEmptyConfig()
		_ = update.SaveChannel(cfg, update.ChannelStable)
		mgr := &stubUserConfigManager{config: cfg}

		action := &updateAction{
			flags: &updateFlags{
				channel:            "",
				checkIntervalHours: 12,
			},
			console:       console,
			formatter:     &output.NoneFormatter{},
			writer:        &bytes.Buffer{},
			configManager: mgr,
		}

		result, err := action.Run(t.Context())
		require.NoError(t, err)
		require.NotNil(t, result)

		// No beta notice should appear
		for _, msg := range console.Output() {
			assert.NotContains(t, msg, "Beta", "beta notice should not appear when config exists")
		}
	})
}
