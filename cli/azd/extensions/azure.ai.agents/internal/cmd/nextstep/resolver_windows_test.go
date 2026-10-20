// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCdSuggestionsWindowsShellRoundTrip(t *testing.T) {
	t.Parallel()
	powerShell, err := exec.LookPath("pwsh")
	if err != nil {
		powerShell, err = exec.LookPath("powershell")
	}
	require.NoError(t, err)
	for _, folder := range []string{
		"My Agent", "my~agent", "Bob's Agent", "my-$USER", "agent[1]", "my&agent", "my-%PATH%", "my-!PATH!",
		"@agent", "my,agent", "my^agent",
	} {
		t.Run(folder, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			target := filepath.Join(root, folder)
			require.NoError(t, os.Mkdir(target, 0o700))
			suggestions := CdSuggestions(folder)
			for _, shell := range []string{"PowerShell", "cmd.exe"} {
				t.Run(shell, func(t *testing.T) {
					var command *exec.Cmd
					if shell == "PowerShell" {
						command = exec.CommandContext(t.Context(), powerShell,
							"-NoLogo", "-NoProfile", "-NonInteractive", "-Command",
							suggestions[1].Command+
								"; if (-not $?) { exit 1 }; [Console]::Write((Get-Location).Path)")
					} else {
						if !strings.HasPrefix(suggestions[2].Command, "cd ") {
							t.Skip("cmd.exe cannot safely represent this path")
						}
						command = exec.CommandContext(t.Context(), os.Getenv("ComSpec"))
						// cmd.exe does not use standard argv quote escaping.
						command.SysProcAttr = &syscall.SysProcAttr{
							CmdLine: `cmd.exe /d /v:on /c ` + suggestions[2].Command + " && cd",
						}
					}
					command.Dir = root
					output, err := command.CombinedOutput()
					require.NoError(t, err, "%s", output)
					got, err := os.Stat(strings.TrimSpace(string(output)))
					require.NoError(t, err)
					want, err := os.Stat(target)
					require.NoError(t, err)
					assert.True(t, os.SameFile(got, want), "%s", output)
				})
			}
		})
	}
}
