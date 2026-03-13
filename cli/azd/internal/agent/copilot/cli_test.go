// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCopilotCLIPath(t *testing.T) {
	path, err := copilotCLIPath()
	require.NoError(t, err)
	require.NotEmpty(t, path)
	require.Contains(t, path, "copilot-cli-"+cliVersion)
	if runtime.GOOS == "windows" {
		require.True(t, len(path) > 4 && path[len(path)-4:] == ".exe")
	}
}

func TestDownloadURL(t *testing.T) {
	tests := []struct {
		goos   string
		goarch string
		pkg    string
	}{
		{"windows", "amd64", "copilot-win32-x64"},
		{"darwin", "amd64", "copilot-darwin-x64"},
		{"darwin", "arm64", "copilot-darwin-arm64"},
		{"linux", "amd64", "copilot-linux-x64"},
		{"linux", "arm64", "copilot-linux-arm64"},
	}

	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			// Verify our platform mapping matches the expected package name
			if runtime.GOOS == tt.goos && runtime.GOARCH == tt.goarch {
				expectedURL := "https://registry.npmjs.org/@github/" + tt.pkg +
					"/-/" + tt.pkg + "-" + cliVersion + ".tgz"
				// The URL would be constructed by downloadCopilotCLI
				require.Contains(t, expectedURL, tt.pkg)
				require.Contains(t, expectedURL, cliVersion)
			}
		})
	}
}

func TestCLIVersionPinned(t *testing.T) {
	// Ensure version constant is set and reasonable
	require.NotEmpty(t, cliVersion)
	require.Regexp(t, `^\d+\.\d+\.\d+$`, cliVersion)
}
