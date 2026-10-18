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

// Quote quotes s for the selected shell, even when it contains only plain characters.
func Quote(sh Shell, s string) string {
	switch sh {
	case PowerShell:
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	case Cmd:
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	default:
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
}

func quote(sh Shell, s string) string {
	if s != "" && isPlain(s) {
		return s
	}
	return Quote(sh, s)
}

func isPlain(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-_./:@+,=", r):
		default:
			return false
		}
	}
	return true
}
