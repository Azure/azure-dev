// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scripting

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuoteCmdArg(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{"empty string", "", `""`},
		{"simple arg", "hello", "hello"},
		{"arg with spaces", "hello world", `"hello world"`},
		{"arg with ampersand", "a&b", `"a&b"`},
		{"arg with pipe", "a|b", `"a|b"`},
		{"arg with angle brackets", "<out>", `"<out>"`},
		{"arg with caret", "a^b", `"a^b"`},
		{"arg with percent", "%PATH%", `"%PATH%"`},
		{"safe path", `C:\scripts\run.bat`, `C:\scripts\run.bat`},
		{
			"path with spaces",
			`C:\my scripts\run.bat`,
			`"C:\my scripts\run.bat"`,
		},
		{
			"embedded double quote",
			`he said "hello"`,
			`"he said ""hello"""`,
		},
		{
			"injection via embedded quotes",
			`a" & calc & "`,
			`"a"" & calc & """`,
		},
		{
			"fake pre-quoted injection",
			`"safe" & calc & "x"`,
			`"""safe"" & calc & ""x"""`,
		},
		{"newline stripped", "a\nb", "ab"},
		{"CR stripped", "a\rb", "ab"},
		{"null stripped", "a\x00b", "ab"},
		{"VT stripped", "a\x0Bb", "ab"},
		{"FF stripped", "a\x0Cb", "ab"},
		{"Ctrl-Z stripped", "a\x1Ab", "ab"},
		{"ESC stripped", "a\x1Bb", "ab"},
		{"newline with metachar", "a\n&b", `"a&b"`},
		{"tab character", "a\tb", "\"a\tb\""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quoteCmdArg(tt.arg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestQuotePowerShellArg(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{"empty string", "", "''"},
		{"simple arg", "hello", "'hello'"},
		{"arg with single quote", "it's", "'it''s'"},
		{"multiple quotes", "a'b'c", "'a''b''c'"},
		{"arg with spaces", "hello world", "'hello world'"},
		{"double dash flag", "--skip-sync", "'--skip-sync'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quotePowerShellArg(tt.arg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildPowerShellInlineCommand(t *testing.T) {
	t.Run("no args returns script as-is", func(t *testing.T) {
		e, err := New(Config{})
		require.NoError(t, err)
		got := e.buildPowerShellInlineCommand("Get-Date")
		assert.Equal(t, "Get-Date", got)
	})

	t.Run("with args joins and quotes", func(t *testing.T) {
		e, err := New(Config{Args: []string{"arg1", "it's"}})
		require.NoError(t, err)
		got := e.buildPowerShellInlineCommand("cmd")
		assert.Equal(t, "cmd 'arg1' 'it''s'", got)
	})
}

func TestBuildCommand_BashInline(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	cmd := e.buildCommand(t.Context(), "bash", "echo hi", true)
	require.NotNil(t, cmd)
	assert.Equal(t, "bash", cmd.Args[0])
	assert.Equal(t, "-c", cmd.Args[1])
	assert.Equal(t, "echo hi", cmd.Args[2])
	assert.Equal(t, "--", cmd.Args[3])
}

func TestBuildCommand_BashFile(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	cmd := e.buildCommand(
		t.Context(), "bash", "/path/to/script.sh", false,
	)
	require.NotNil(t, cmd)
	assert.Equal(t, "bash", cmd.Args[0])
	assert.Equal(t, "/path/to/script.sh", cmd.Args[1])
}

func TestBuildCommand_PwshInline(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	cmd := e.buildCommand(
		t.Context(), "pwsh", "Write-Host 'hi'", true,
	)
	require.NotNil(t, cmd)
	assert.Equal(t, "pwsh", cmd.Args[0])
	assert.Equal(t, "-Command", cmd.Args[1])
	assert.Equal(t, "Write-Host 'hi'", cmd.Args[2])
}

func TestBuildCommand_PwshFile(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	cmd := e.buildCommand(
		t.Context(), "pwsh", "/path/to/script.ps1", false,
	)
	require.NotNil(t, cmd)
	assert.Equal(t, "pwsh", cmd.Args[0])
	assert.Equal(t, "-File", cmd.Args[1])
	assert.Equal(t, "/path/to/script.ps1", cmd.Args[2])
}

func TestBuildCommand_CmdInline(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	cmd := e.buildCommand(
		t.Context(), "cmd", "echo hello", true,
	)
	require.NotNil(t, cmd)
	assert.Equal(t, "cmd", cmd.Args[0])
	assert.Equal(t, "/c", cmd.Args[1])
	assert.Equal(t, "echo hello", cmd.Args[2])
}

func TestBuildCommand_CmdFile(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	cmd := e.buildCommand(
		t.Context(), "cmd", `C:\test.bat`, false,
	)
	require.NotNil(t, cmd)
	assert.Equal(t, "cmd", cmd.Args[0])
	assert.Equal(t, "/c", cmd.Args[1])
	assert.Equal(t, `"C:\test.bat"`, cmd.Args[2])
}

func TestBuildCommand_WithArgs(t *testing.T) {
	e, err := New(Config{Args: []string{"arg1", "arg2"}})
	require.NoError(t, err)

	cmd := e.buildCommand(
		t.Context(), "bash", "/path/to/script.sh", false,
	)
	require.NotNil(t, cmd)
	assert.True(t, slices.Contains(cmd.Args, "arg1"))
	assert.True(t, slices.Contains(cmd.Args, "arg2"))
}

func TestBuildCommand_PwshInline_SkipsArgs(t *testing.T) {
	e, err := New(Config{Args: []string{"a1"}})
	require.NoError(t, err)

	cmd := e.buildCommand(
		t.Context(), "pwsh", "Get-Date", true,
	)
	require.NotNil(t, cmd)
	// Args should be baked into the -Command string, not appended
	assert.Len(t, cmd.Args, 3)
	assert.Contains(t, cmd.Args[2], "'a1'")
}

func TestBuildCommand_DefaultShell(t *testing.T) {
	e, err := New(Config{})
	require.NoError(t, err)

	cmd := e.buildCommand(
		t.Context(), "unknown-shell", "echo hi", true,
	)
	require.NotNil(t, cmd)
	// Unknown shells fall through to default case
	assert.Equal(t, "unknown-shell", cmd.Args[0])
	assert.Equal(t, "-c", cmd.Args[1])
}

func TestBuildCommand_ShellCaseNormalization(t *testing.T) {
	tests := []struct {
		shell     string
		wantFirst string
	}{
		{"BASH", "bash"},
		{"SH", "sh"},
		{"ZSH", "zsh"},
		{"PWSH", "pwsh"},
		{"CMD", "cmd"},
	}

	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			e, err := New(Config{Shell: tt.shell})
			require.NoError(t, err)

			cmd := e.buildCommand(
				t.Context(), tt.shell, "test.sh", false,
			)
			require.NotNil(t, cmd)
			assert.Equal(t, tt.wantFirst, cmd.Args[0])
		})
	}
}
