// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"

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
		"@agent", "my,agent", "my^agent", "-agent",
		"\u2013agent", "\u2014agent", "\u2015agent",
		"Bob\u2018s Agent", "Bob\u2018sAgent", "Bob\u2019s Agent", "Bob\u2019sAgent",
		"Bob\u201as Agent", "Bob\u201asAgent", "Bob\u201bs Agent", "Bob\u201bsAgent",
		"my\u201cagent", "my\u201cagent\u201c", "my \u201cagent",
		"my\u201dagent", "my\u201dagent\u201d", "my \u201dagent",
		"my\u201eagent", "my\u201eagent\u201e", "my \u201eagent",
		"my\u201fagent", "my\u201fagent\u201f", "my \u201fagent",
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
							"[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new(); "+
								suggestions[1].Command+
								"; if (-not $?) { exit 1 }; [Console]::Write((Get-Location).Path)")
					} else {
						if !strings.HasPrefix(suggestions[2].Command, "cd ") {
							t.Skip("cmd.exe cannot safely represent this path")
						}
						command = exec.CommandContext(t.Context(), os.Getenv("ComSpec")) //nolint:gosec // test-only: ComSpec runs controlled fixture commands
						// cmd.exe does not use standard argv quote escaping.
						command.SysProcAttr = &syscall.SysProcAttr{
							CmdLine: `cmd.exe /d /u /v:on /c ` + suggestions[2].Command + " && cd",
						}
					}
					command.Dir = root
					output, err := command.CombinedOutput()
					require.NoError(t, err, "%s", output)
					if shell == "cmd.exe" {
						output = decodeCmdOutput(t, output)
					}
					got, err := os.Stat(strings.TrimSpace(string(output))) //nolint:gosec // test-only: path is the shell round-trip output for a temp dir
					require.NoError(t, err)
					want, err := os.Stat(target)
					require.NoError(t, err)
					assert.True(t, os.SameFile(got, want), "%s", output)
				})
			}
		})
	}
}

func TestCdSuggestionsWindowsAbsolutePath(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "agent-\u6d4b\u8bd5")
	require.NoError(t, os.Mkdir(root, 0o700))
	target := filepath.Join(root, "agent")
	require.NoError(t, os.Mkdir(target, 0o700))
	suggestions := CdSuggestions(filepath.ToSlash(target))
	require.Len(t, suggestions, 3)
	require.Equal(t, "cd /d \""+filepath.ToSlash(target)+"\"", suggestions[2].Command)
	command := exec.CommandContext(t.Context(), os.Getenv("ComSpec")) //nolint:gosec // test-only: ComSpec runs controlled fixture commands
	command.Dir = root
	command.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `cmd.exe /d /u /c ` + suggestions[2].Command + " && cd",
	}
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	output = decodeCmdOutput(t, output)
	got, err := os.Stat(strings.TrimSpace(string(output))) //nolint:gosec // test-only: path is the shell round-trip output for a temp dir
	require.NoError(t, err)
	want, err := os.Stat(target)
	require.NoError(t, err)
	assert.True(t, os.SameFile(got, want))
}

func decodeCmdOutput(t *testing.T, output []byte) []byte {
	t.Helper()
	require.Zero(t, len(output)%2)
	chars := make([]uint16, len(output)/2)
	for i := range chars {
		chars[i] = binary.LittleEndian.Uint16(output[i*2:])
	}
	return []byte(string(utf16.Decode(chars)))
}
