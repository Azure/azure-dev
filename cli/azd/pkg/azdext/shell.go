// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ---------------------------------------------------------------------------
// Shell detection and execution
// ---------------------------------------------------------------------------

// ShellType represents a detected shell environment.
type ShellType string

const (
	// ShellTypeBash is the Bourne Again Shell.
	ShellTypeBash ShellType = "bash"
	// ShellTypeSh is the POSIX shell.
	ShellTypeSh ShellType = "sh"
	// ShellTypeZsh is the Z Shell.
	ShellTypeZsh ShellType = "zsh"
	// ShellTypeFish is the Fish shell.
	ShellTypeFish ShellType = "fish"
	// ShellTypePowerShell is PowerShell (pwsh/powershell.exe).
	ShellTypePowerShell ShellType = "powershell"
	// ShellTypeCmd is Windows cmd.exe.
	ShellTypeCmd ShellType = "cmd"
	// ShellTypeUnknown indicates the shell could not be determined.
	ShellTypeUnknown ShellType = ""
)

// String returns the string representation of the shell type.
func (s ShellType) String() string {
	if s == ShellTypeUnknown {
		return "unknown"
	}
	return string(s)
}

// ShellInfo contains information about the detected shell.
type ShellInfo struct {
	// Type is the detected shell type.
	Type ShellType
	// Path is the filesystem path to the shell executable, if known.
	Path string
	// Source describes how the shell was detected.
	Source string
}

// DetectShell identifies the current shell environment.
//
// Detection strategy (in order):
//  1. SHELL environment variable (Unix) — most reliable on macOS/Linux.
//  2. PSModulePath environment variable — indicates PowerShell on any platform.
//  3. ComSpec environment variable (Windows) — standard Windows shell path.
//  4. Platform default fallback (sh on Unix, cmd on Windows).
//
// Platform behavior:
//   - Windows: Detects cmd.exe (default), PowerShell, or WSL shells.
//   - macOS/Linux: Detects from $SHELL (bash, zsh, fish, sh).
//   - If $SHELL is unset, falls back to platform default.
//
// DetectShell never returns an error. If detection fails, Type is [ShellTypeUnknown].
func DetectShell() ShellInfo {
	// Strategy 1: $SHELL (Unix convention, also set in some Windows terminals).
	if shellEnv := os.Getenv("SHELL"); shellEnv != "" {
		st := shellTypeFromPath(shellEnv)
		if st != ShellTypeUnknown {
			return ShellInfo{Type: st, Path: shellEnv, Source: "SHELL"}
		}
	}

	// Strategy 2: PSModulePath indicates PowerShell is the active shell.
	if psPath := os.Getenv("PSModulePath"); psPath != "" {
		// Try to find pwsh/powershell on PATH.
		if p, err := exec.LookPath("pwsh"); err == nil {
			return ShellInfo{Type: ShellTypePowerShell, Path: p, Source: "PSModulePath"}
		}
		if p, err := exec.LookPath("powershell"); err == nil {
			return ShellInfo{Type: ShellTypePowerShell, Path: p, Source: "PSModulePath"}
		}
		return ShellInfo{Type: ShellTypePowerShell, Path: "", Source: "PSModulePath"}
	}

	// Strategy 3: ComSpec (Windows).
	if comspec := os.Getenv("ComSpec"); comspec != "" {
		st := shellTypeFromPath(comspec)
		if st != ShellTypeUnknown {
			return ShellInfo{Type: st, Path: comspec, Source: "ComSpec"}
		}
		// ComSpec is set but not a recognized shell; assume cmd.
		return ShellInfo{Type: ShellTypeCmd, Path: comspec, Source: "ComSpec"}
	}

	// Strategy 4: Platform default.
	if runtime.GOOS == "windows" {
		return ShellInfo{Type: ShellTypeCmd, Path: "", Source: "platform-default"}
	}
	return ShellInfo{Type: ShellTypeSh, Path: "/bin/sh", Source: "platform-default"}
}

// ShellCommand creates an [exec.Cmd] that executes script through the
// appropriate shell for the current platform.
//
// Platform behavior:
//   - Windows cmd: cmd.exe /C <script>
//   - PowerShell:  pwsh -NoProfile -NonInteractive -Command <script>
//   - Unix shells: <shell> -c <script>
//
// The returned Cmd inherits the provided context for cancellation and timeout.
// The caller is responsible for setting Stdin, Stdout, Stderr, Dir, and Env
// on the returned Cmd before running it.
//
// Returns an error if the shell type is unknown and no fallback is available.
//
// Security note: script is passed directly to the shell and may contain
// arbitrary commands. Callers MUST NOT pass unsanitized user input as the
// script argument. For executing a known program with arguments (no shell
// interpolation), use [ExecCommand] instead.
func ShellCommand(ctx context.Context, script string) (*exec.Cmd, error) {
	info := DetectShell()
	return ShellCommandWith(ctx, info, script)
}

// ShellCommandWith creates an [exec.Cmd] using the specified [ShellInfo].
// This allows callers to override shell detection for testing or when a
// specific shell is required.
//
// See [ShellCommand] for platform behavior details.
func ShellCommandWith(ctx context.Context, info ShellInfo, script string) (*exec.Cmd, error) {
	switch info.Type {
	case ShellTypeCmd:
		shell := info.Path
		if shell == "" {
			shell = "cmd.exe"
		}
		// #nosec G204 -- executing caller-provided scripts is the purpose of this helper.
		return exec.CommandContext(ctx, shell, "/C", script), nil

	case ShellTypePowerShell:
		shell := info.Path
		if shell == "" {
			// Prefer pwsh (cross-platform PowerShell) over powershell (Windows-only).
			if p, err := exec.LookPath("pwsh"); err == nil {
				shell = p
			} else if p, err := exec.LookPath("powershell"); err == nil {
				shell = p
			} else {
				shell = "pwsh"
			}
		}
		// #nosec G204 -- executing caller-provided scripts is the purpose of this helper.
		return exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-Command", script), nil

	case ShellTypeBash, ShellTypeSh, ShellTypeZsh, ShellTypeFish:
		shell := info.Path
		if shell == "" {
			shell = string(info.Type)
		}
		// #nosec G204 -- executing caller-provided scripts is the purpose of this helper.
		return exec.CommandContext(ctx, shell, "-c", script), nil

	case ShellTypeUnknown:
		// Last-resort fallback based on platform.
		if runtime.GOOS == "windows" {
			return exec.CommandContext(ctx, "cmd.exe", "/C", script), nil
		}
		return exec.CommandContext(ctx, "/bin/sh", "-c", script), nil

	default:
		return nil, fmt.Errorf("azdext.ShellCommand: unsupported shell type %q", info.Type)
	}
}

// IsInteractiveTerminal reports whether the given file descriptor is connected
// to an interactive terminal (TTY).
//
// Platform behavior:
//   - Unix: Uses [os.File.Stat] to check for character device mode.
//   - Windows: Uses [os.File.Stat] to check for character device mode.
//
// This function is safe to call with nil (returns false).
func IsInteractiveTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// IsStdinTerminal reports whether standard input is an interactive terminal.
func IsStdinTerminal() bool {
	return IsInteractiveTerminal(os.Stdin)
}

// IsStdoutTerminal reports whether standard output is an interactive terminal.
func IsStdoutTerminal() bool {
	return IsInteractiveTerminal(os.Stdout)
}

// ExecCommand creates an [exec.Cmd] that runs a program directly without a
// shell, preventing shell injection. Arguments are passed as a list, not
// interpolated through a shell parser.
//
// This is the recommended API for executing external programs when the
// program path and arguments are known. Use [ShellCommand] only when shell
// features (pipes, globbing, variable expansion) are genuinely required.
//
// The name is resolved via [exec.LookPath]-style lookup (PATH search).
func ExecCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// shellTypeFromPath extracts the shell type from a filesystem path.
func shellTypeFromPath(path string) ShellType {
	// Normalize: extract base name and remove .exe suffix.
	base := path
	if idx := strings.LastIndexAny(path, `/\`); idx >= 0 {
		base = path[idx+1:]
	}
	base = strings.TrimSuffix(strings.ToLower(base), ".exe")

	switch base {
	case "bash":
		return ShellTypeBash
	case "sh", "dash", "ash":
		return ShellTypeSh
	case "zsh":
		return ShellTypeZsh
	case "fish":
		return ShellTypeFish
	case "pwsh", "powershell":
		return ShellTypePowerShell
	case "cmd":
		return ShellTypeCmd
	default:
		return ShellTypeUnknown
	}
}
