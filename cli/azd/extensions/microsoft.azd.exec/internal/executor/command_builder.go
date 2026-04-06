// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package executor

import (
	"context"
	"os/exec"
	"strings"

	"microsoft.azd.exec/internal/shellutil"
)

// buildCommand builds the exec.Cmd for the given shell and script, respecting
// the provided context for cancellation and deadline propagation.
func (e *Executor) buildCommand(ctx context.Context, shell, scriptOrPath string, isInline bool) *exec.Cmd {
	var cmdArgs []string
	skipAppendArgs := false
	useCmdLineOverride := false
	cmdWrapOuter := false

	shellLower := strings.ToLower(shell)

	shellBin := shell
	if shellutil.ValidShells[shellLower] {
		shellBin = shellLower
	}

	switch shellLower {
	case "bash", "sh", "zsh":
		if isInline {
			cmdArgs = []string{shellBin, "-c", scriptOrPath, "--"}
		} else {
			cmdArgs = []string{shellBin, scriptOrPath}
		}
	case "pwsh", "powershell":
		if isInline {
			cmdArgs = []string{shellBin, "-Command", e.buildPowerShellInlineCommand(scriptOrPath)}
			skipAppendArgs = true
		} else {
			cmdArgs = []string{shellBin, "-File", scriptOrPath}
		}
	case "cmd":
		// All cmd.exe paths use CmdLine override to bypass Go's
		// CommandLineToArgvW escaping which is incompatible with cmd.exe.
		useCmdLineOverride = true
		if isInline {
			cmdArgs = []string{shellBin, "/c", scriptOrPath}
			cmdWrapOuter = false
		} else {
			cmdArgs = []string{shellBin, "/c", `"` + scriptOrPath + `"`}
			cmdWrapOuter = true
		}
	default:
		// All valid shells are handled above. This branch is unreachable
		// when the shell has been validated by New(). Guard defensively.
		if isInline {
			cmdArgs = []string{shell, "-c", scriptOrPath, "--"}
		} else {
			cmdArgs = []string{shell, scriptOrPath}
		}
	}

	if !skipAppendArgs && len(e.config.Args) > 0 {
		if useCmdLineOverride {
			// Quote each arg individually for cmd.exe metacharacter safety
			for _, arg := range e.config.Args {
				cmdArgs = append(cmdArgs, quoteCmdArg(arg))
			}
		} else {
			cmdArgs = append(cmdArgs, e.config.Args...)
		}
	}

	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...) //nolint:gosec // CLI command builder; args validated upstream
	if useCmdLineOverride {
		setCmdLineOverride(cmd, cmdArgs, cmdWrapOuter)
	}
	return cmd
}

// controlCharReplacer strips control characters that cmd.exe interprets as
// command separators or word boundaries:
//   - \n, \r, \x00: command terminators
//   - \x0B (VT), \x0C (FF): treated as whitespace by cmd.exe parser
//   - \x1A: Ctrl+Z / MS-DOS EOF marker
//   - \x1B: ESC (ANSI sequence prefix)
var controlCharReplacer = strings.NewReplacer(
	"\n", "", "\r", "", "\x00", "",
	"\x0B", "", "\x0C", "",
	"\x1A", "", "\x1B", "",
)

// quoteCmdArg quotes a single argument for cmd.exe if it contains spaces,
// tabs, or metacharacters. Embedded double quotes are escaped by doubling them.
// Newline/CR/null bytes are stripped as they act as command separators.
// NOTE: cmd.exe expands %VAR% patterns even inside double quotes — this is an
// inherent limitation with no general workaround.
func quoteCmdArg(arg string) string {
	if arg == "" {
		return `""`
	}
	cleaned := controlCharReplacer.Replace(arg)
	// Escape embedded double quotes by doubling (cmd.exe convention)
	escaped := strings.ReplaceAll(cleaned, `"`, `""`)
	if strings.ContainsAny(escaped, " \t&|<>^%\"") {
		return `"` + escaped + `"`
	}
	return escaped
}

// buildPowerShellInlineCommand joins the inline script with its arguments into a single -Command string.
func (e *Executor) buildPowerShellInlineCommand(scriptOrPath string) string {
	if len(e.config.Args) == 0 {
		return scriptOrPath
	}

	quotedArgs := make([]string, len(e.config.Args))
	for i, arg := range e.config.Args {
		quotedArgs[i] = quotePowerShellArg(arg)
	}

	return strings.Join(append([]string{scriptOrPath}, quotedArgs...), " ")
}

// quotePowerShellArg returns a safely single-quoted PowerShell argument.
func quotePowerShellArg(arg string) string {
	if arg == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(arg, "'", "''") + "'"
}
