// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/installer"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
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
		err := action.persistNonChannelFlags(cfg)
		require.NoError(t, err)
	})

	t.Run("interval_set", func(t *testing.T) {
		t.Parallel()

		action := &updateAction{
			flags: &updateFlags{
				checkIntervalHours: 12,
			},
			configManager: &simpleConfigMgr{},
		}

		cfg := config.NewEmptyConfig()
		err := action.persistNonChannelFlags(cfg)
		require.NoError(t, err)

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

func Test_NewUpdateAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &updateFlags{}
	console := mockinput.NewMockConsole()
	formatter := &output.JsonFormatter{}
	a := newUpdateAction(flags, console, formatter, io.Discard, nil, nil)
	ua := a.(*updateAction)
	require.Same(t, flags, ua.flags)
}

func Test_NewUpdateAction(t *testing.T) {
	t.Parallel()
	action := newUpdateAction(
		&updateFlags{},
		mockinput.NewMockConsole(),
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // configManager
		nil, // commandRunner
	)
	require.NotNil(t, action)
}

func Test_NewUpdateAction_Fields(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	formatter := &output.NoneFormatter{}
	writer := &bytes.Buffer{}
	flags := &updateFlags{}
	action := newUpdateAction(flags, console, formatter, writer, nil, nil)
	require.NotNil(t, action)
}

func Test_NewUpdateCmd(t *testing.T) {
	t.Parallel()
	cmd := newUpdateCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "update", cmd.Use)
}

func Test_UpdateFlags_Bind(t *testing.T) {
	t.Parallel()
	flags := &updateFlags{}
	cmd := newUpdateCmd()
	global := &internal.GlobalCommandOptions{}
	flags.Bind(cmd.Flags(), global)
	assert.Equal(t, global, flags.global)
}

func newTestUpdateAction(
	flags *updateFlags,
	console input.Console,
	formatter output.Formatter,
	writer *bytes.Buffer,
	cfgMgr config.UserConfigManager,
	cmdRunner exec.CommandRunner,
) *updateAction {
	return &updateAction{
		flags:         flags,
		console:       console,
		formatter:     formatter,
		writer:        writer,
		configManager: cfgMgr,
		commandRunner: cmdRunner,
		httpClient:    failingHTTPClient(),
	}
}

func Test_UpdateAction_Run_OnlyConfigFlags_AlphaNotEnabled(t *testing.T) {
	// Tests the path: IsNonProdVersion()=false -> alpha not enabled -> auto-enable ->
	// onlyConfigFlagsSet path saves config preferences.
	setProdVersion(t)
	clearCIEnv(t)

	cfgMgr := &simpleConfigMgr{}
	console := mockinput.NewMockConsole()
	var buf bytes.Buffer

	flags := &updateFlags{
		channel:            "",
		checkIntervalHours: 12,
	}

	action := newTestUpdateAction(flags, console, &output.JsonFormatter{}, &buf, cfgMgr, &noopCommandRunner{})
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Message.Header, "Update preferences saved")
}

func Test_UpdateAction_Run_OnlyConfigFlags_AlphaEnabled(t *testing.T) {
	// Tests path when alpha IS already enabled and only config flags set
	setProdVersion(t)
	clearCIEnv(t)

	// Pre-enable the update alpha feature
	cfg := config.NewEmptyConfig()
	_ = cfg.Set("alpha.update", "on")
	cfgMgr := &simpleConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	var buf bytes.Buffer

	flags := &updateFlags{
		channel:            "",
		checkIntervalHours: 24,
	}

	action := newTestUpdateAction(flags, console, &output.JsonFormatter{}, &buf, cfgMgr, &noopCommandRunner{})
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Message.Header, "Update preferences saved")
}

func Test_UpdateAction_Run_SwitchChannel_CheckForUpdateError(t *testing.T) {
	// Tests channel switch that triggers CheckForUpdate (which will fail via noopCommandRunner)
	setProdVersion(t)

	cfg := config.NewEmptyConfig()
	_ = cfg.Set("alpha.update", "on")
	cfgMgr := &simpleConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	// Handle any Confirm prompts (like "Switch from stable to daily?")
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(true)
	var buf bytes.Buffer

	flags := &updateFlags{
		channel: "daily",
	}

	action := newTestUpdateAction(flags, console, &output.JsonFormatter{}, &buf, cfgMgr, &noopCommandRunner{})
	_, err := action.Run(t.Context())
	// This will either fail at CI check, package manager check, or CheckForUpdate
	require.Error(t, err)
}

func Test_UpdateAction_Run_NoChannelNoConfigFlags(t *testing.T) {
	// Tests path: no channel, no config flags -> onlyConfigFlagsSet()=false -> goes to CheckForUpdate
	setProdVersion(t)

	cfg := config.NewEmptyConfig()
	_ = cfg.Set("alpha.update", "on")
	cfgMgr := &simpleConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	var buf bytes.Buffer

	// No channel, no checkIntervalHours => onlyConfigFlagsSet() == false
	flags := &updateFlags{}

	action := newTestUpdateAction(flags, console, &output.JsonFormatter{}, &buf, cfgMgr, &noopCommandRunner{})
	_, err := action.Run(t.Context())
	// Will fail at CI check or CheckForUpdate since noopCommandRunner returns error
	require.Error(t, err)
}

// A misleading version promise for brew, winget, or choco users is exactly the mismatch
// this messaging exists to avoid, so both branches are asserted.
// See https://github.com/Azure/azure-dev/issues/9145.
func Test_UpdateMessages_NameVersionOnlyWhenPinnable(t *testing.T) {
	t.Parallel()

	target := &update.VersionInfo{Channel: update.ChannelStable, Version: "1.28.1"}

	tests := []struct {
		name         string
		installedBy  installer.InstallType
		wantTitle    string
		wantHeader   string
		wantNoDigits bool
	}{
		{
			name:        "install script pins the resolved version",
			installedBy: installer.InstallTypeSh,
			wantTitle:   "Updating azd to 1.28.1 (stable)",
			wantHeader:  "Updated azd to version 1.28.1! Changes take effect on next invocation.",
		},
		{
			name:        "ps1 install pins the resolved version",
			installedBy: installer.InstallTypePs,
			wantTitle:   "Updating azd to 1.28.1 (stable)",
			wantHeader:  "Updated azd to version 1.28.1! Changes take effect on next invocation.",
		},
		{
			name:        "deb install pins the resolved version",
			installedBy: installer.InstallTypeDeb,
			wantTitle:   "Updating azd to 1.28.1 (stable)",
			wantHeader:  "Updated azd to version 1.28.1! Changes take effect on next invocation.",
		},
		{
			name:         "brew names no version",
			installedBy:  installer.InstallTypeBrew,
			wantTitle:    "Updating azd via brew (stable)",
			wantHeader:   "Updated azd! Changes take effect on next invocation.",
			wantNoDigits: true,
		},
		{
			name:         "winget names no version",
			installedBy:  installer.InstallTypeWinget,
			wantTitle:    "Updating azd via winget (stable)",
			wantHeader:   "Updated azd! Changes take effect on next invocation.",
			wantNoDigits: true,
		},
		{
			name:         "choco names no version",
			installedBy:  installer.InstallTypeChoco,
			wantTitle:    "Updating azd via choco (stable)",
			wantHeader:   "Updated azd! Changes take effect on next invocation.",
			wantNoDigits: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			title := updateTitle(tt.installedBy, target)
			header := updatedHeader(tt.installedBy, target)

			require.Equal(t, tt.wantTitle, title)
			require.Equal(t, tt.wantHeader, header)

			if tt.wantNoDigits {
				require.NotContains(t, title, target.Version)
				require.NotContains(t, header, target.Version)
			}
		})
	}
}

func Test_UpdateMessages_Daily(t *testing.T) {
	t.Parallel()

	target := &update.VersionInfo{Channel: update.ChannelDaily, Version: "1.29.0-beta.1-daily.6614998"}

	require.Equal(t,
		"Updating azd to 1.29.0-beta.1-daily.6614998 (daily)",
		updateTitle(installer.InstallTypeSh, target))
	require.Equal(t,
		"Updating azd via brew (daily)",
		updateTitle(installer.InstallTypeBrew, target))
}

func Test_UpdateAction_OnlyConfigFlagsSet(t *testing.T) {
	t.Parallel()
	// True: no channel, positive interval
	a := &updateAction{flags: &updateFlags{channel: "", checkIntervalHours: 10}}
	require.True(t, a.onlyConfigFlagsSet())

	// False: channel set
	a2 := &updateAction{flags: &updateFlags{channel: "stable", checkIntervalHours: 10}}
	require.False(t, a2.onlyConfigFlagsSet())

	// False: no channel, zero interval
	a3 := &updateAction{flags: &updateFlags{channel: "", checkIntervalHours: 0}}
	require.False(t, a3.onlyConfigFlagsSet())
}

func Test_UpdateAction_PersistNonChannelFlags(t *testing.T) {
	t.Parallel()

	// Test with positive check interval
	a := &updateAction{
		flags:         &updateFlags{checkIntervalHours: 24},
		configManager: &simpleConfigMgr{},
	}
	cfg := config.NewEmptyConfig()
	err := a.persistNonChannelFlags(cfg)
	require.NoError(t, err)

	// Test with zero check interval
	a2 := &updateAction{flags: &updateFlags{checkIntervalHours: 0}}
	cfg2 := config.NewEmptyConfig()
	err = a2.persistNonChannelFlags(cfg2)
	require.NoError(t, err)
}

func Test_NewUpdateFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newUpdateFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_UpdateAction_Run_NonProdVersion(t *testing.T) {
	// In test builds, IsNonProdVersion() returns true, so Run exits immediately.
	console := mockinput.NewMockConsole()
	a := newUpdateAction(
		&updateFlags{},
		console,
		&output.JsonFormatter{},
		&bytes.Buffer{},
		&testConfigMgr{},
		nil, // commandRunner not needed – early exit
	)

	_, err := a.(*updateAction).Run(t.Context())
	require.Error(t, err)
	assert.True(t, errors.Is(err, internal.ErrUnsupportedOperation))
}
