// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/installer"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
)

// disableTerminalFormatting suppresses ANSI hyperlink and color escape codes
// so substring assertions on the rendered banner are deterministic regardless
// of whether `go test` is attached to a TTY. See
// pkg/update/banner_test.go:disableTerminalFormatting for the rationale on
// why color.NoColor is set directly instead of via NO_COLOR.
func disableTerminalFormatting(t *testing.T) {
	t.Helper()
	t.Setenv("AZD_FORCE_TTY", "false")
	prev := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = prev })
}

func TestPlatformUpgradeHintFor(t *testing.T) {
	disableTerminalFormatting(t)

	const detailsMarker = "If you installed azd with custom options"

	tests := []struct {
		name        string
		goos        string
		installedBy installer.InstallType
		channel     update.Channel
		// wantContains are substrings expected in the rendered banner's
		// "To update, ..." line and any details paragraph.
		wantContains []string
		// wantNotContains guards against accidental inclusion of the
		// details paragraph for hint types that shouldn't carry one.
		wantNotContains []string
	}{
		// Windows
		{
			name:        "windows/ps/stable",
			goos:        "windows",
			installedBy: installer.InstallTypePs,
			channel:     update.ChannelStable,
			wantContains: []string{
				"To update, run",
				"Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression",
				detailsMarker,
				"https://aka.ms/azd/upgrade/windows",
			},
		},
		{
			name:        "windows/ps/daily uses scriptblock and -Version daily",
			goos:        "windows",
			installedBy: installer.InstallTypePs,
			channel:     update.ChannelDaily,
			wantContains: []string{
				"[scriptblock]::Create((Invoke-RestMethod 'https://aka.ms/install-azd.ps1'))) -Version 'daily'",
				detailsMarker,
				"https://aka.ms/azd/upgrade/windows",
			},
		},
		{
			name:            "windows/winget ignores channel",
			goos:            "windows",
			installedBy:     installer.InstallTypeWinget,
			channel:         update.ChannelDaily,
			wantContains:    []string{"To update, run", "winget upgrade Microsoft.Azd"},
			wantNotContains: []string{detailsMarker},
		},
		{
			name:            "windows/choco ignores channel",
			goos:            "windows",
			installedBy:     installer.InstallTypeChoco,
			channel:         update.ChannelDaily,
			wantContains:    []string{"choco upgrade azd"},
			wantNotContains: []string{detailsMarker},
		},
		{
			name:         "windows/unknown falls back to docs",
			goos:         "windows",
			installedBy:  installer.InstallTypeUnknown,
			channel:      update.ChannelStable,
			wantContains: []string{"To update, visit", "https://aka.ms/azd/upgrade/windows"},
		},
		// Linux
		{
			name:        "linux/sh/stable",
			goos:        "linux",
			installedBy: installer.InstallTypeSh,
			channel:     update.ChannelStable,
			wantContains: []string{
				"curl -fsSL https://aka.ms/install-azd.sh | bash",
				detailsMarker,
				"https://aka.ms/azd/upgrade/linux",
			},
			wantNotContains: []string{"--version daily"},
		},
		{
			name:        "linux/sh/daily appends --version daily",
			goos:        "linux",
			installedBy: installer.InstallTypeSh,
			channel:     update.ChannelDaily,
			wantContains: []string{
				"curl -fsSL https://aka.ms/install-azd.sh | bash -s -- --version daily",
				detailsMarker,
				"https://aka.ms/azd/upgrade/linux",
			},
		},
		{
			name:         "linux/unknown falls back to docs",
			goos:         "linux",
			installedBy:  installer.InstallTypeUnknown,
			channel:      update.ChannelStable,
			wantContains: []string{"To update, visit", "https://aka.ms/azd/upgrade/linux"},
		},
		// Darwin
		{
			name:            "darwin/brew ignores channel",
			goos:            "darwin",
			installedBy:     installer.InstallTypeBrew,
			channel:         update.ChannelDaily,
			wantContains:    []string{"brew uninstall azd && brew install --cask azure/azd/azd"},
			wantNotContains: []string{detailsMarker},
		},
		{
			name:        "darwin/sh/stable",
			goos:        "darwin",
			installedBy: installer.InstallTypeSh,
			channel:     update.ChannelStable,
			wantContains: []string{
				"curl -fsSL https://aka.ms/install-azd.sh | bash",
				detailsMarker,
				"https://aka.ms/azd/upgrade/mac",
			},
			wantNotContains: []string{"--version daily"},
		},
		{
			name:        "darwin/sh/daily appends --version daily",
			goos:        "darwin",
			installedBy: installer.InstallTypeSh,
			channel:     update.ChannelDaily,
			wantContains: []string{
				"curl -fsSL https://aka.ms/install-azd.sh | bash -s -- --version daily",
				detailsMarker,
				"https://aka.ms/azd/upgrade/mac",
			},
		},
		{
			name:         "darwin/unknown falls back to docs",
			goos:         "darwin",
			installedBy:  installer.InstallTypeUnknown,
			channel:      update.ChannelStable,
			wantContains: []string{"To update, visit", "https://aka.ms/azd/upgrade/mac"},
		},
		// Unrecognized OS
		{
			name:         "unknown OS falls back to generic docs",
			goos:         "plan9",
			installedBy:  installer.InstallTypeUnknown,
			channel:      update.ChannelStable,
			wantContains: []string{"To update, visit", "https://aka.ms/azd/upgrade"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := platformUpgradeHintFor(tt.goos, tt.installedBy, tt.channel)
			rendered := update.RenderUpdateBanner(update.BannerParams{
				CurrentVersion: "1.0.0",
				LatestVersion:  "1.1.0",
				Channel:        tt.channel,
				UpdateHint:     hint,
			})
			for _, want := range tt.wantContains {
				assert.Contains(t, rendered, want)
			}
			for _, dontWant := range tt.wantNotContains {
				assert.NotContains(t, rendered, dontWant)
			}
		})
	}
}
