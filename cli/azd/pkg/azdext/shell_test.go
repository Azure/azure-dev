// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"os"
	"runtime"
	"testing"
)

// ---------------------------------------------------------------------------
// ShellType.String
// ---------------------------------------------------------------------------

func TestShellType_String(t *testing.T) {
	tests := []struct {
		st   ShellType
		want string
	}{
		{ShellTypeBash, "bash"},
		{ShellTypeSh, "sh"},
		{ShellTypeZsh, "zsh"},
		{ShellTypeFish, "fish"},
		{ShellTypePowerShell, "powershell"},
		{ShellTypeCmd, "cmd"},
		{ShellTypeUnknown, "unknown"},
	}
	for _, tc := range tests {
		if got := tc.st.String(); got != tc.want {
			t.Errorf("ShellType(%q).String() = %q, want %q", string(tc.st), got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// shellTypeFromPath
// ---------------------------------------------------------------------------

func TestShellTypeFromPath(t *testing.T) {
	tests := []struct {
		path string
		want ShellType
	}{
		// Unix-style paths.
		{"/bin/bash", ShellTypeBash},
		{"/usr/bin/zsh", ShellTypeZsh},
		{"/bin/sh", ShellTypeSh},
		{"/usr/bin/fish", ShellTypeFish},
		{"/usr/local/bin/pwsh", ShellTypePowerShell},
		{"/usr/bin/dash", ShellTypeSh},
		{"/usr/bin/ash", ShellTypeSh},

		// Windows-style paths.
		{`C:\Windows\System32\cmd.exe`, ShellTypeCmd},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, ShellTypePowerShell},
		{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, ShellTypePowerShell},

		// Just basenames.
		{"bash", ShellTypeBash},
		{"zsh", ShellTypeZsh},
		{"pwsh", ShellTypePowerShell},
		{"cmd.exe", ShellTypeCmd},

		// Unknown.
		{"/usr/bin/unknown-shell", ShellTypeUnknown},
		{"", ShellTypeUnknown},
		{"python3", ShellTypeUnknown},
	}
	for _, tc := range tests {
		got := shellTypeFromPath(tc.path)
		if got != tc.want {
			t.Errorf("shellTypeFromPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// DetectShell
// ---------------------------------------------------------------------------

func TestDetectShell_SHELLEnv(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")
	// Ensure PSModulePath and ComSpec don't interfere.
	t.Setenv("PSModulePath", "")
	os.Unsetenv("PSModulePath")

	info := DetectShell()
	if info.Type != ShellTypeBash {
		t.Errorf("DetectShell() with SHELL=/bin/bash: Type = %q, want %q", info.Type, ShellTypeBash)
	}
	if info.Path != "/bin/bash" {
		t.Errorf("DetectShell() with SHELL=/bin/bash: Path = %q, want %q", info.Path, "/bin/bash")
	}
	if info.Source != "SHELL" {
		t.Errorf("DetectShell() with SHELL=/bin/bash: Source = %q, want %q", info.Source, "SHELL")
	}
}

func TestDetectShell_PlatformDefault(t *testing.T) {
	// Clear all shell-related env vars.
	t.Setenv("SHELL", "")
	os.Unsetenv("SHELL")
	t.Setenv("PSModulePath", "")
	os.Unsetenv("PSModulePath")
	t.Setenv("ComSpec", "")
	os.Unsetenv("ComSpec")

	info := DetectShell()
	if info.Source != "platform-default" {
		t.Errorf("DetectShell() with no shell env: Source = %q, want %q", info.Source, "platform-default")
	}

	switch runtime.GOOS {
	case "windows":
		if info.Type != ShellTypeCmd {
			t.Errorf("DetectShell() on Windows: Type = %q, want %q", info.Type, ShellTypeCmd)
		}
	default:
		if info.Type != ShellTypeSh {
			t.Errorf("DetectShell() on Unix: Type = %q, want %q", info.Type, ShellTypeSh)
		}
	}
}

func TestDetectShell_ComSpec(t *testing.T) {
	// Only relevant context: ComSpec set, SHELL unset.
	t.Setenv("SHELL", "")
	os.Unsetenv("SHELL")
	t.Setenv("PSModulePath", "")
	os.Unsetenv("PSModulePath")
	t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)

	info := DetectShell()
	if info.Type != ShellTypeCmd {
		t.Errorf("DetectShell() with ComSpec: Type = %q, want %q", info.Type, ShellTypeCmd)
	}
	if info.Source != "ComSpec" {
		t.Errorf("DetectShell() with ComSpec: Source = %q, want %q", info.Source, "ComSpec")
	}
}

// ---------------------------------------------------------------------------
// ShellCommand / ShellCommandWith
// ---------------------------------------------------------------------------

func TestShellCommandWith_Bash(t *testing.T) {
	info := ShellInfo{Type: ShellTypeBash, Path: "/bin/bash"}
	cmd, err := ShellCommandWith(context.Background(), info, "echo hello")
	if err != nil {
		t.Fatalf("ShellCommandWith(bash) error: %v", err)
	}
	if cmd.Path == "" {
		t.Error("ShellCommandWith(bash) returned empty cmd.Path")
	}
	// Check args contain "-c" and the script.
	found := false
	for _, arg := range cmd.Args {
		if arg == "echo hello" {
			found = true
		}
	}
	if !found {
		t.Errorf("ShellCommandWith(bash) args %v do not contain script", cmd.Args)
	}
}

func TestShellCommandWith_Cmd(t *testing.T) {
	info := ShellInfo{Type: ShellTypeCmd, Path: "cmd.exe"}
	cmd, err := ShellCommandWith(context.Background(), info, "echo hello")
	if err != nil {
		t.Fatalf("ShellCommandWith(cmd) error: %v", err)
	}
	// Check args contain "/C" and the script.
	foundC := false
	foundScript := false
	for _, arg := range cmd.Args {
		if arg == "/C" {
			foundC = true
		}
		if arg == "echo hello" {
			foundScript = true
		}
	}
	if !foundC || !foundScript {
		t.Errorf("ShellCommandWith(cmd) args %v missing /C or script", cmd.Args)
	}
}

func TestShellCommandWith_PowerShell(t *testing.T) {
	info := ShellInfo{Type: ShellTypePowerShell, Path: "pwsh"}
	cmd, err := ShellCommandWith(context.Background(), info, "Write-Host hello")
	if err != nil {
		t.Fatalf("ShellCommandWith(powershell) error: %v", err)
	}
	// Check args contain "-Command" and "-NoProfile".
	hasCommand := false
	hasNoProfile := false
	for _, arg := range cmd.Args {
		if arg == "-Command" {
			hasCommand = true
		}
		if arg == "-NoProfile" {
			hasNoProfile = true
		}
	}
	if !hasCommand {
		t.Errorf("ShellCommandWith(powershell) args %v missing -Command", cmd.Args)
	}
	if !hasNoProfile {
		t.Errorf("ShellCommandWith(powershell) args %v missing -NoProfile", cmd.Args)
	}
}

func TestShellCommandWith_Unknown(t *testing.T) {
	// Unknown shell should fall back to platform default.
	info := ShellInfo{Type: ShellTypeUnknown}
	cmd, err := ShellCommandWith(context.Background(), info, "echo test")
	if err != nil {
		t.Fatalf("ShellCommandWith(unknown) error: %v", err)
	}
	if cmd == nil {
		t.Fatal("ShellCommandWith(unknown) returned nil cmd")
	}
}

// ---------------------------------------------------------------------------
// IsInteractiveTerminal
// ---------------------------------------------------------------------------

func TestIsInteractiveTerminal_Nil(t *testing.T) {
	if IsInteractiveTerminal(nil) {
		t.Error("IsInteractiveTerminal(nil) = true, want false")
	}
}

func TestIsInteractiveTerminal_RegularFile(t *testing.T) {
	// A regular file (temp file) is not a terminal.
	f, err := os.CreateTemp(t.TempDir(), "tty-test-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()

	if IsInteractiveTerminal(f) {
		t.Error("IsInteractiveTerminal(regular file) = true, want false")
	}
}

// In CI, stdin/stdout may not be terminals. We test the function doesn't panic.
func TestIsStdinTerminal_NoPanic(t *testing.T) {
	_ = IsStdinTerminal()
}

func TestIsStdoutTerminal_NoPanic(t *testing.T) {
	_ = IsStdoutTerminal()
}
