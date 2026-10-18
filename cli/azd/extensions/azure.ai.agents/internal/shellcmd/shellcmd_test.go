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
	names := append(append([]string{}, hostile...), "@splat", "a,b", "at@mid", "x=y")
	for _, name := range names {
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
		{"%PATH%", "'%PATH%'", "'%PATH%'", unsupportedCmdLiteral},
		{"!name!", "'!name!'", "'!name!'", unsupportedCmdLiteral},
		{"a%b", "'a%b'", "'a%b'", unsupportedCmdLiteral},
		{"a!b", "'a!b'", "'a!b'", unsupportedCmdLiteral},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			require.Equal(t, tt.posix, Quote(POSIX, tt.value))
			require.Equal(t, tt.powerShell, Quote(PowerShell, tt.value))
			require.Equal(t, tt.cmd, Quote(Cmd, tt.value))
		})
	}
}

func TestCanLiteral(t *testing.T) {
	require.True(t, CanLiteral(POSIX, "%PATH%"))
	require.True(t, CanLiteral(PowerShell, "%PATH%"))
	require.True(t, CanLiteral(Cmd, "plain"))
	require.True(t, CanLiteral(Cmd, `a"b`))
	require.False(t, CanLiteral(Cmd, "%PATH%"))
	require.False(t, CanLiteral(Cmd, "!name!"))
	require.False(t, CanLiteral(Cmd, "a%b"))
	require.False(t, CanLiteral(Cmd, "a!b"))
}

func TestIsPlainAndRender(t *testing.T) {
	plainAll := []string{"agent-1", "-_./:+=", "https://example.com/azure.yaml"}
	for _, value := range plainAll {
		t.Run("all/"+value, func(t *testing.T) {
			for _, sh := range []Shell{POSIX, PowerShell, Cmd} {
				require.True(t, isPlain(sh, value), "shell %v", sh)
				require.Equal(t, "tool "+value, Command("tool", Lit(value)).Render(sh))
			}
		})
	}

	// @ and , are safe unquoted in POSIX/Cmd but must be quoted in PowerShell.
	psSpecial := []string{"@splat", "a,b", "-_./:@+,="}
	for _, value := range psSpecial {
		t.Run("ps-special/"+value, func(t *testing.T) {
			require.True(t, isPlain(POSIX, value))
			require.True(t, isPlain(Cmd, value))
			require.False(t, isPlain(PowerShell, value))
			require.Equal(t, "tool "+value, Command("tool", Lit(value)).Render(POSIX))
			require.Equal(t, "tool "+value, Command("tool", Lit(value)).Render(Cmd))
			require.Equal(t, "tool "+Quote(PowerShell, value), Command("tool", Lit(value)).Render(PowerShell))
		})
	}

	hostileArgs := append(hostile, `"quoted"`, `C:\agent`, "*.yaml", "x&y", "x|y", "x>y", "%PATH%", "!x!", "a\nb")
	for _, value := range hostileArgs {
		t.Run("hostile/"+value, func(t *testing.T) {
			for _, sh := range []Shell{POSIX, PowerShell, Cmd} {
				require.False(t, isPlain(sh, value), "shell %v", sh)
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
	require.Equal(t, "tool "+unsupportedCmdLiteral, Command("tool", Lit("%PATH%")).Render(Cmd))
	require.Equal(t, "tool "+unsupportedCmdLiteral, Command("tool", Lit("!x!")).Render(Cmd))
}

func TestPowerShellSplatAndCommaRoundTrip(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not installed")
	}
	// Unquoted @ is splat (cd @name → home) and unquoted , builds arrays; quoting keeps them literal.
	for _, name := range []string{"@splat", "a,b"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, name)
			require.NoError(t, os.Mkdir(dir, 0o755))

			unquoted := "cd " + name + "; (Get-Location).ProviderPath"
			bad := exec.CommandContext(t.Context(), pwsh, "-NoProfile", "-Command", unquoted)
			bad.Dir = root
			out, _ := bad.Output()
			require.NotEqual(t, dir, strings.TrimRight(string(out), "\r\n"),
				"unquoted %q must not land in the target dir (script: %s, out: %q)", name, unquoted, out)

			script := Cd(name).Render(PowerShell) + "; (Get-Location).ProviderPath"
			cmd := exec.CommandContext(t.Context(), pwsh, "-NoProfile", "-Command", script)
			cmd.Dir = root
			out, err := cmd.Output()
			require.NoError(t, err, "script: %s", script)
			require.Equal(t, dir, strings.TrimRight(string(out), "\r\n"))
		})
	}
}

func TestCommandPOSIXRoundTrip(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not installed")
	}
	values := append([]string{"", "plain", "line\nbreak", `"double"`, `back\slash`, "*?[abc]", "x&y|z>out", "@splat", "a,b", "%PATH%", "!x!"}, hostile...)
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

func TestCmdPercentNotExpandedByDoubleQuotesAlone(t *testing.T) {
	// Document the bug Quote used to have: wrapping in "..." does not stop
	// %VAR% expansion. We refuse those values instead of emitting a lying quote.
	require.Equal(t, `"%PATH%"`, `"`+strings.ReplaceAll("%PATH%", `"`, `""`)+`"`)
	require.NotEqual(t, `"%PATH%"`, Quote(Cmd, "%PATH%"))
	require.Equal(t, unsupportedCmdLiteral, Quote(Cmd, "%PATH%"))
}
