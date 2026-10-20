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
		"@agent", "my,agent", "my^agent",
		"Bob\u2018s Agent", "Bob\u2018sAgent", "Bob\u2019s Agent", "Bob\u2019sAgent",
		"Bob\u201as Agent", "Bob\u201asAgent", "Bob\u201bs Agent", "Bob\u201bsAgent",
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
							CmdLine: `cmd.exe /d /u /v:on /c ` + suggestions[2].Command + " && cd",
						}
					}
					command.Dir = root
					output, err := command.CombinedOutput()
					require.NoError(t, err, "%s", output)
					if shell == "cmd.exe" {
						require.Zero(t, len(output)%2)
						chars := make([]uint16, len(output)/2)
						for i := range chars {
							chars[i] = binary.LittleEndian.Uint16(output[i*2:])
						}
						output = []byte(string(utf16.Decode(chars)))
					}
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

func TestCdSuggestionsWindowsAbsolutePath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "agent")
	require.NoError(t, os.Mkdir(target, 0o700))
	suggestions := CdSuggestions(filepath.ToSlash(target))
	require.Len(t, suggestions, 3)
	require.Equal(t, "cd /d \""+filepath.ToSlash(target)+"\"", suggestions[2].Command)
	command := exec.CommandContext(t.Context(), os.Getenv("ComSpec"))
	command.Dir = root
	command.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `cmd.exe /d /c ` + suggestions[2].Command + " && cd",
	}
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	got, err := os.Stat(strings.TrimSpace(string(output)))
	require.NoError(t, err)
	want, err := os.Stat(target)
	require.NoError(t, err)
	assert.True(t, os.SameFile(got, want))
}
