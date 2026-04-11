// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scripting

import (
	"context"
	"os/exec"
	"strings"
)

func (e *Executor) buildCommand(
	ctx context.Context, shell, scriptOrPath string, isInline bool,
) *exec.Cmd {
	var cmdArgs []string
	skipAppendArgs := false
	useCmdLineOverride := false
	cmdWrapOuter := false

	shellLower := strings.ToLower(shell)
	shellBin := shell
	if IsSupportedShell(shellLower) {
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
			cmdArgs = []string{
				shellBin, "-Command",
				e.buildPowerShellInlineCommand(scriptOrPath),
			}
			skipAppendArgs = true
		} else {
			cmdArgs = []string{shellBin, "-File", scriptOrPath}
		}
	case "cmd":
		useCmdLineOverride = true
		if isInline {
			cmdArgs = []string{shellBin, "/c", scriptOrPath}
			cmdWrapOuter = false
		} else {
			escaped := strings.ReplaceAll(scriptOrPath, `"`, `""`)
			cmdArgs = []string{shellBin, "/c", `"` + escaped + `"`}
			cmdWrapOuter = true
		}
	default:
		if isInline {
			cmdArgs = []string{shell, "-c", scriptOrPath, "--"}
		} else {
			cmdArgs = []string{shell, scriptOrPath}
		}
	}

	if !skipAppendArgs && len(e.config.Args) > 0 {
		if useCmdLineOverride {
			for _, arg := range e.config.Args {
				cmdArgs = append(cmdArgs, quoteCmdArg(arg))
			}
		} else {
			cmdArgs = append(cmdArgs, e.config.Args...)
		}
	}

	cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...) //nolint:gosec
	if useCmdLineOverride {
		setCmdLineOverride(cmd, cmdArgs, cmdWrapOuter)
	}
	return cmd
}

// stripControlChars removes all ASCII control characters (0x00–0x1F, 0x7F)
// from s. Tab (0x09) is included because cmd.exe treats it as whitespace
// that can break argument boundaries.
func stripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7F {
			return -1
		}
		return r
	}, s)
}

// quoteCmdArg quotes a single argument for cmd.exe.
func quoteCmdArg(arg string) string {
	if arg == "" {
		return `""`
	}
	cleaned := stripControlChars(arg)
	escaped := strings.ReplaceAll(cleaned, `"`, `""`)
	if strings.ContainsAny(escaped, " \t&|<>^%\"") {
		return `"` + escaped + `"`
	}
	return escaped
}

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

func quotePowerShellArg(arg string) string {
	if arg == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(arg, "'", "''") + "'"
}