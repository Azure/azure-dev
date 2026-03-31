// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package executor

import (
	"os/exec"
	"slices"
	"testing"
)

func TestBuildCommandWithCustomShell(t *testing.T) {
	tests := []struct {
		name       string
		shell      string
		scriptPath string
		args       []string
		wantFirst  string
	}{
		{
			name:       "Custom shell python",
			shell:      "python3",
			scriptPath: "script.py",
			args:       []string{"arg1"},
			wantFirst:  "python3",
		},
		{
			name:       "Custom shell node",
			shell:      "node",
			scriptPath: "script.js",
			args:       nil,
			wantFirst:  "node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := New(Config{Args: tt.args})
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}
			cmd := e.buildCommand(t.Context(), tt.shell, tt.scriptPath, false)

			if cmd == nil {
				t.Fatal("buildCommand returned nil")
			}

			found := slices.Contains(cmd.Args, tt.wantFirst)

			if !found {
				t.Errorf("buildCommand args don't contain shell %v: %v", tt.wantFirst, cmd.Args)
			}
		})
	}
}

func TestBuildCommandShellVariations(t *testing.T) {
	tests := []struct {
		shell      string
		scriptPath string
	}{
		{shell: "SH", scriptPath: "test.sh"},
		{shell: "BASH", scriptPath: "test.sh"},
	}

	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			e, err := New(Config{Shell: tt.shell})
			if err != nil {
				t.Fatalf("New() error: %v", err)
			}
			cmd := e.buildCommand(t.Context(), tt.shell, tt.scriptPath, false)

			if cmd == nil {
				t.Fatal("buildCommand returned nil")
			}
		})
	}
}

func TestBuildCommandLookPath(t *testing.T) {
	e, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	cmd := e.buildCommand(t.Context(), "cmd", "test.bat", false)

	if cmd.Path == "" {
		t.Error("buildCommand created command with empty Path")
	}

	_, err = exec.LookPath(cmd.Args[0])
	if err != nil {
		t.Logf("Command %v not found in PATH (may be platform-specific)", cmd.Args[0])
	}
}

func TestQuotePowerShellArg(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{name: "empty string", arg: "", want: "''"},
		{name: "simple arg", arg: "hello", want: "'hello'"},
		{name: "arg with single quote", arg: "it's", want: "'it''s'"},
		{name: "arg with multiple quotes", arg: "a'b'c", want: "'a''b''c'"},
		{name: "arg with double dash", arg: "--skip-sync", want: "'--skip-sync'"},
		{name: "arg with spaces", arg: "hello world", want: "'hello world'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quotePowerShellArg(tt.arg)
			if got != tt.want {
				t.Errorf("quotePowerShellArg(%q) = %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}

func TestBuildPowerShellInlineCommand(t *testing.T) {
	t.Run("no args returns script as-is", func(t *testing.T) {
		e, err := New(Config{})
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		got := e.buildPowerShellInlineCommand("Get-Date")
		if got != "Get-Date" {
			t.Errorf("got %q, want %q", got, "Get-Date")
		}
	})

	t.Run("with args joins and quotes", func(t *testing.T) {
		e, err := New(Config{Args: []string{"arg1", "it's"}})
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		got := e.buildPowerShellInlineCommand("cmd")
		want := "cmd 'arg1' 'it''s'"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestQuoteCmdArg(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string
	}{
		{name: "empty string", arg: "", want: `""`},
		{name: "simple arg", arg: "hello", want: "hello"},
		{name: "arg with spaces", arg: "hello world", want: `"hello world"`},
		{name: "arg with ampersand", arg: "a&b", want: `"a&b"`},
		{name: "arg with pipe", arg: "a|b", want: `"a|b"`},
		{name: "arg with angle brackets", arg: "<out>", want: `"<out>"`},
		{name: "arg with caret", arg: "a^b", want: `"a^b"`},
		{name: "arg with percent", arg: "%PATH%", want: `"%PATH%"`},
		{name: "safe path", arg: `C:\scripts\run.bat`, want: `C:\scripts\run.bat`},
		{name: "path with spaces", arg: `C:\my scripts\run.bat`, want: `"C:\my scripts\run.bat"`},
		{name: "path with ampersand", arg: `C:\a&b\run.bat`, want: `"C:\a&b\run.bat"`},
		// Embedded double-quote injection: quotes are escaped by doubling
		{name: "embedded double quote", arg: `he said "hello"`, want: `"he said ""hello"""`},
		{name: "injection via embedded quotes", arg: `a" & calc & "`, want: `"a"" & calc & """`},
		// Previously-quoted input must NOT be trusted (CWE-78)
		{name: "fake pre-quoted injection", arg: `"safe" & calc & "x"`, want: `"""safe"" & calc & ""x"""`},
		// Newline/CR/null stripping (CWE-93)
		{name: "newline stripped", arg: "a\nb", want: "ab"},
		{name: "CR stripped", arg: "a\rb", want: "ab"},
		{name: "null stripped", arg: "a\x00b", want: "ab"},
		{name: "newline with metachar", arg: "a\n&b", want: `"a&b"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quoteCmdArg(tt.arg)
			if got != tt.want {
				t.Errorf("quoteCmdArg(%q) = %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}
