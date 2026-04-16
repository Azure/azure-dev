// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderUpdateBanner(t *testing.T) {
	t.Parallel()

	params := BannerParams{
		CurrentVersion: "1.11.0",
		LatestVersion:  "1.13.1",
		Channel:        ChannelStable,
		UpdateHint:     RunUpdateHint("azd update"),
	}

	result := RenderUpdateBanner(params)
	require.NotEmpty(t, result)

	for _, s := range []string{
		"Update available:",
		"1.11.0 -> 1.13.1",
		"To update, run `azd update`",
		// WithHyperlink falls back to plain URL in non-terminal test environments.
		"github.com/Azure/azure-dev/releases/tag/azure-dev-cli_1.13.1",
	} {
		assert.Contains(t, result, s, "expected banner to contain %q", s)
	}

	// The legacy phrasing should not appear after the refactor.
	assert.NotContains(t, result, "WARNING:")
	assert.NotContains(t, result, "out of date")
}

func TestRenderUpdateBanner_PlatformCommand(t *testing.T) {
	t.Parallel()

	t.Run("run_hint_with_details", func(t *testing.T) {
		t.Parallel()
		params := BannerParams{
			CurrentVersion: "1.11.0",
			LatestVersion:  "1.13.1",
			Channel:        ChannelStable,
			UpdateHint: RunUpdateHint(
				"curl -fsSL https://aka.ms/install-azd.sh | bash",
				WithDetails("If you installed azd with custom options, see https://aka.ms/azd/upgrade/linux for details."),
			),
		}
		result := RenderUpdateBanner(params)
		assert.Contains(t, result, "To update, run `curl -fsSL https://aka.ms/install-azd.sh | bash`")
		assert.Contains(t, result, "If you installed azd with custom options")
		assert.Contains(t, result, "https://aka.ms/azd/upgrade/linux")
	})

	t.Run("handles_visit_url", func(t *testing.T) {
		t.Parallel()
		visitParams := BannerParams{
			CurrentVersion: "1.11.0",
			LatestVersion:  "1.13.1",
			Channel:        ChannelStable,
			UpdateHint:     VisitUpdateHint("https://aka.ms/azd/upgrade/linux"),
		}
		result := RenderUpdateBanner(visitParams)
		assert.Contains(t, result, "To update, visit https://aka.ms/azd/upgrade/linux")
	})
}

func TestRenderUpdateBanner_DailyChannel(t *testing.T) {
	t.Parallel()

	// Daily version strings already embed the build number (e.g.
	// "1.24.0-daily.6168094"), so no extra formatting is needed.
	params := BannerParams{
		CurrentVersion: "1.11.0",
		LatestVersion:  "1.24.0-daily.6168094",
		Channel:        ChannelDaily,
		UpdateHint:     RunUpdateHint("azd update"),
	}

	result := RenderUpdateBanner(params)
	assert.Contains(t, result, "Update available:")
	assert.Contains(t, result, "1.24.0-daily.6168094")
	assert.Contains(t, result, "github.com/Azure/azure-dev/commits/main/")
	// Daily banner omits the "current -> latest" format.
	assert.NotContains(t, result, "1.11.0 ->")
}

func TestFormatUpdateHint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    UpdateHint
		contains []string
	}{
		{
			name:     "simple_command",
			input:    RunUpdateHint("azd update"),
			contains: []string{"To update, run `azd update`"},
		},
		{
			name:  "shell_command",
			input: RunUpdateHint("curl -fsSL https://aka.ms/install-azd.sh | bash"),
			contains: []string{
				"To update, run `curl -fsSL https://aka.ms/install-azd.sh | bash`",
			},
		},
		{
			name:     "visit_url",
			input:    VisitUpdateHint("https://aka.ms/azd/upgrade/linux"),
			contains: []string{"To update, visit https://aka.ms/azd/upgrade/linux"},
		},
		{
			name:     "winget_command",
			input:    RunUpdateHint("winget upgrade Microsoft.Azd"),
			contains: []string{"To update, run `winget upgrade Microsoft.Azd`"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := formatUpdateHint(tt.input)
			for _, s := range tt.contains {
				assert.Contains(t, got, s, "expected hint to contain %q", s)
			}
		})
	}
}

func TestFormatUpdateHint_EmptyRendersNothing(t *testing.T) {
	t.Parallel()
	assert.Empty(t, formatUpdateHint(UpdateHint{}))
}

func TestReleaseNotesLink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		params   BannerParams
		expected releaseNotesLink
	}{
		{
			name: "stable_links_to_release_tag",
			params: BannerParams{
				LatestVersion: "1.13.1",
				Channel:       ChannelStable,
			},
			expected: releaseNotesLink{
				label: "Release Notes",
				url:   "https://github.com/Azure/azure-dev/releases/tag/azure-dev-cli_1.13.1",
			},
		},
		{
			name: "daily_links_to_commits",
			params: BannerParams{
				LatestVersion: "1.24.0-daily.6168094",
				Channel:       ChannelDaily,
			},
			expected: releaseNotesLink{
				label: "Recent Changes",
				url:   "https://github.com/Azure/azure-dev/commits/main",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.params.releaseNotesLink()
			assert.Equal(t, tt.expected, got)
		})
	}
}
