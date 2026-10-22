// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package shellcmd renders copyable command lines for a specific shell.
package shellcmd

import "strings"

// Shell identifies the quoting rules used to render a command.
type Shell int

const (
	// POSIX uses single quotes with backslash-escaped apostrophes.
	POSIX Shell = iota
	// PowerShell uses single quotes with doubled apostrophes.
	PowerShell
	// Cmd uses double quotes with doubled embedded double quotes.
	Cmd
)

// unsupportedCmdLiteral stands in for a value Cmd cannot make literal.
//
// %VAR% and !VAR! still expand inside Cmd "...", and no pasteable escaping is
// reliable for both interactive cmd and .bat. Like evaluations ShellArg, the
// printed command then fails on the name rather than expanding it.
const unsupportedCmdLiteral = "VALUE_NEEDS_QUOTING"

// Arg is one literal argument that the shell must not expand.
type Arg struct{ v string }

// Lit constructs a literal argument.
func Lit(s string) Arg { return Arg{s} }

// Line is a rendered-on-demand command. Its zero value renders as "".
type Line struct {
	prog string
	args []Arg
}

// Command constructs a command from a trusted program name and literal arguments.
func Command(prog string, args ...Arg) Line { return Line{prog: prog, args: args} }

// Cd constructs a change-directory command.
func Cd(dir string) Line { return Command("cd", Lit(dir)) }

// String renders the command using POSIX quoting.
func (l Line) String() string { return l.Render(POSIX) }

// Render renders the command using the selected shell's quoting rules.
func (l Line) Render(sh Shell) string {
	if l.prog == "" {
		return ""
	}
	parts := []string{l.prog}
	for _, a := range l.args {
		parts = append(parts, quote(sh, a.v))
	}
	return strings.Join(parts, " ")
}

// CanLiteral reports whether Quote can make s a true literal for sh.
//
// Cmd cannot: % and ! still expand inside double quotes.
func CanLiteral(sh Shell, s string) bool {
	return sh != Cmd || !strings.ContainsAny(s, "%!")
}

// Quote quotes s for the selected shell, even when it contains only plain characters.
//
// For Cmd, values containing % or ! cannot be made literal; Quote returns
// VALUE_NEEDS_QUOTING so a pasted command fails on the name instead of expanding.
func Quote(sh Shell, s string) string {
	switch sh {
	case PowerShell:
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	case Cmd:
		if !CanLiteral(Cmd, s) {
			return unsupportedCmdLiteral
		}
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	default:
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
}

func quote(sh Shell, s string) string {
	if s != "" && isPlain(sh, s) {
		return s
	}
	return Quote(sh, s)
}

// isPlain reports whether s can stay unquoted for sh.
//
// The safe unquoted set is shell-dependent: PowerShell treats leading @ as
// splat and , as array construction, so those must be quoted there even though
// they are fine unquoted in POSIX and Cmd.
func isPlain(sh Shell, s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case isPlainPunct(sh, r):
		default:
			return false
		}
	}
	return true
}

func isPlainPunct(sh Shell, r rune) bool {
	switch sh {
	case PowerShell:
		return strings.ContainsRune("-_./:+=", r)
	default:
		return strings.ContainsRune("-_./:@+,=", r)
	}
}
