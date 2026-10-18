// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package shellcmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var hostile = []string{
	"my agent", "my-$USER", "it's", "tab\there", "back`tick`", "my~agent", "a;b", "$(echo x)",
}

func TestCdPOSIXRoundTrip(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, name)
			require.NoError(t, os.Mkdir(dir, 0o755))
			script := Cd(name).Render(POSIX) + " && pwd -P"
			cmd := exec.CommandContext(t.Context(), sh, "-c", script)
			cmd.Dir = root
			out, err := cmd.Output()
			require.NoError(t, err, "script: %s", script)
			want, err := filepath.EvalSymlinks(dir)
			require.NoError(t, err)
			require.Equal(t, want+"\n", string(out))
		})
	}
}

func TestCdPowerShellRoundTrip(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not installed")
	}
	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, name)
			require.NoError(t, os.Mkdir(dir, 0o755))
			script := Cd(name).Render(PowerShell) + "; (Get-Location).ProviderPath"
			cmd := exec.CommandContext(t.Context(), pwsh, "-NoProfile", "-Command", script)
			cmd.Dir = root
			out, err := cmd.Output()
			require.NoError(t, err, "script: %s", script)
			require.Equal(t, dir, strings.TrimRight(string(out), "\r\n"))
		})
	}
}

func TestGoQuoteIsNotShellSafe(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	root := t.TempDir()
	name := "my-$USER"
	require.NoError(t, os.Mkdir(filepath.Join(root, name), 0o755))
	t.Setenv("USER", "someone")
	cmd := exec.CommandContext(t.Context(), sh, "-c", fmt.Sprintf("cd %q", name))
	cmd.Dir = root
	require.Error(t, cmd.Run(), "Go-quoted cd must expand $USER and fail")
}

func TestQuote(t *testing.T) {
	tests := []struct {
		value      string
		posix      string
		powerShell string
		cmd        string
	}{
		{"", "''", "''", `""`},
		{"plain", "'plain'", "'plain'", `"plain"`},
		{"it's", `'it'\''s'`, "'it''s'", `"it's"`},
		{`a"b`, `'a"b'`, `'a"b'`, `"a""b"`},
		{"$USER `echo x`", "'$USER `echo x`'", "'$USER `echo x`'", "\"$USER `echo x`\""},
		{`C:\agent templates\azure.yaml`, `'C:\agent templates\azure.yaml'`,
			`'C:\agent templates\azure.yaml'`, `"C:\agent templates\azure.yaml"`},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			require.Equal(t, tt.posix, Quote(POSIX, tt.value))
			require.Equal(t, tt.powerShell, Quote(PowerShell, tt.value))
			require.Equal(t, tt.cmd, Quote(Cmd, tt.value))
		})
	}
}

func TestIsPlainAndRender(t *testing.T) {
	for _, value := range []string{"agent-1", "-_./:@+,=", "https://example.com/azure.yaml"} {
		t.Run(value, func(t *testing.T) {
			require.True(t, isPlain(value))
			for _, sh := range []Shell{POSIX, PowerShell, Cmd} {
				require.Equal(t, "tool "+value, Command("tool", Lit(value)).Render(sh))
			}
		})
	}
	hostileArgs := append(hostile, `"quoted"`, `C:\agent`, "*.yaml", "x&y", "x|y", "x>y", "%PATH%", "!x!", "a\nb")
	for _, value := range hostileArgs {
		t.Run(value, func(t *testing.T) {
			require.False(t, isPlain(value))
			for _, sh := range []Shell{POSIX, PowerShell, Cmd} {
				require.Equal(t, "tool "+Quote(sh, value), Command("tool", Lit(value)).Render(sh))
			}
		})
	}
	require.Equal(t, "tool ''", Command("tool", Lit("")).String())
	require.Equal(t, "cd 'it'\\''s'", Cd("it's").String())
	require.Empty(t, Line{}.String())
	require.Equal(t, "tool", Command("tool").String())
	require.Equal(t, "tool --name 'prod west' ''",
		Command("tool", Lit("--name"), Lit("prod west"), Lit("")).String())
}

func TestCommandPOSIXRoundTrip(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	values := append([]string{"", "plain", "line\nbreak", `"double"`, `back\slash`, "*?[abc]", "x&y|z>out"}, hostile...)
	args := []Arg{Lit("%s\\000")}
	for _, value := range values {
		args = append(args, Lit(value))
	}
	cmd := exec.CommandContext(t.Context(), sh, "-c", Command("printf", args...).String())
	cmd.Dir = t.TempDir()
	out, err := cmd.Output()
	require.NoError(t, err)
	require.Equal(t, strings.Join(values, "\x00")+"\x00", string(out))
}
