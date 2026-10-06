// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Double quotes do not make a value literal: $ and backticks still expand inside
// them, and \" does not escape a quote in PowerShell. These values come out of
// the configuration file, so a printed step that wrapped one in double quotes
// would run it when pasted. Single quotes are literal in POSIX shells and in
// PowerShell, so off Windows a value with nothing that any of them reads
// specially inside them is printed as itself, single-quoted, rather than as a
// placeholder nobody can run.
func TestShellArgSingleQuotesWhatDoubleQuotesCannotMakeLiteralOffWindows(t *testing.T) {
	for in, want := range map[string]string{
		"./eval$dir":                  `'./eval$dir'`,
		"$(whoami)":                   `'$(whoami)'`,
		"a`whoami`b":                  "'a`whoami`b'",
		"${HOME}":                     `'${HOME}'`,
		"/home/me/my evals/$x/a.yaml": `'/home/me/my evals/$x/a.yaml'`,
		"$x&calc":                     `'$x&calc'`,
		"a$\nb":                       "'a$\nb'",
	} {
		assert.Equal(t, want, shellArgFor("linux", in), "%q on a POSIX shell", in)
		assert.Equal(t, want, shellArgFor("darwin", in), "%q on macOS", in)
	}
}

// On Windows nothing printed says whether cmd.exe or PowerShell is pasting it.
// Single quotes are literal in PowerShell but ordinary characters in cmd.exe,
// which splits a quoted value at its spaces, so a value that needs literal
// quoting is named instead of printed in a form only one of them runs. cmd.exe
// also expands %NAME% inside double quotes, reads an unquoted ^ as an escape and
// ends the command at a line break, so those are named whatever else the value
// carries.
func TestShellArgNamesWhatNoWindowsShellReadsAlike(t *testing.T) {
	for _, v := range []string{
		"./eval$dir", "$(whoami)", "a`whoami`b", `say "hi"`, `C:\Users\Me\My $dir\a.yaml`, "it's $x",
		"$x&calc", "$x|calc", "$x>out", `x"y$`,
		"%TEMP%", `C:\Users\%USERNAME%\a.yaml`, "a^b", "a^&b", "my evals\nrun", "100%",
	} {
		assert.Equal(t, "VALUE_NEEDS_QUOTING", shellArgFor("windows", v), "%q on Windows", v)
	}
	// A control character has no quoting anywhere.
	for _, v := range []string{"a\x00b", "a\x1bb", "a\rb"} {
		for _, goos := range []string{"linux", "windows"} {
			assert.Equal(t, "VALUE_NEEDS_QUOTING", shellArgFor(goos, v), "%q has no quoting on %s", v, goos)
		}
	}
	// The line break and % are only a problem for cmd.exe.
	assert.Equal(t, "\"my evals\nrun\"", shellArgFor("linux", "my evals\nrun"))
	assert.Equal(t, "100%", shellArgFor("linux", "100%"))
	assert.Equal(t, "a^b", shellArgFor("linux", "a^b"))
}

// Off Windows an ASCII ' has no escape that bash, zsh and PowerShell all read
// the same way, and fish reads \' and \\ inside single quotes, so a quote or a
// backslash is not printed beside a $, a backtick or a double quote.
func TestShellArgNamesAQuoteOrBackslashBesideWhatNeedsLiteralQuotingOffWindows(t *testing.T) {
	for _, v := range []string{"it's $x", `$x';calc;'`, `a\b$x`, `$x\`, `\';calc;\'$`, `say "hi" it's`} {
		for _, goos := range []string{"linux", "darwin"} {
			assert.Equal(t, "VALUE_NEEDS_QUOTING", shellArgFor(goos, v),
				"%q has no single escape every POSIX shell and PowerShell read alike on %s", v, goos)
		}
	}
}

// PowerShell reads the typographic quotes as quotes on every operating system,
// so they end a quoted value early whichever way the value is wrapped.
func TestShellArgNamesTheTypographicQuotesPowerShellReads(t *testing.T) {
	for _, v := range []string{
		"a$x\u2019; calc; \u2018b", "\u2018$x", "$x\u201a", "$x\u201b",
		"a\u201d; calc; \u201cb c", "my \u201cevals\u201d", "$x\u201e", "plain\u201fname",
	} {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			assert.Equal(t, "VALUE_NEEDS_QUOTING", shellArgFor(goos, v),
				"%q holds a character PowerShell reads as a quote on %s", v, goos)
		}
	}
}

// What is printed is either the value, carried whole between single quotes
// with its quoting balanced, or the placeholder, never a half-quoted form that
// a shell would read differently.
func TestAPrintedValueIsEitherQuotedWholeOrThePlaceholder(t *testing.T) {
	values := []string{
		`C:\Users\Me\run$1\azure.eval.yaml`,
		`/tmp/it's a "test"/azure.eval.yaml`,
		"$x';Write-Output INJECTED;'",
		"a$x\u2019; Write-Output INJECTED; \u2018b",
		`x"y$`,
		`\';calc;\'$`,
		"/home/me/my evals/run$1/azure.eval.yaml",
	}
	for _, v := range values {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			got := shellArgFor(goos, v)
			if got == "VALUE_NEEDS_QUOTING" {
				continue
			}
			assert.NotEqual(t, "windows", goos, "%q needs literal quoting, which no Windows shell reads alike", v)
			assert.True(t, strings.HasPrefix(got, "'") && strings.HasSuffix(got, "'"), "%q on %s: %s", v, goos, got)
			assert.Equal(t, v, got[1:len(got)-1], "%q on %s is carried whole", v, goos)
			assert.NotContains(t, got[1:len(got)-1], "'", "%q on %s keeps its quoting balanced", v, goos)
		}
	}
}

// The command a caller prints must run as printed, so a path that needs only
// double quotes is the real path and never the placeholder on any shell.
func TestAPrintedPathNeedingOnlyDoubleQuotesIsTheRealPath(t *testing.T) {
	for _, path := range []string{
		`C:\Users\Me\My Evals\azure.eval.yaml`,
		"/home/me/my evals/azure.eval.yaml",
		"/tmp/team evals/run (a)/azure.eval.yaml",
	} {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			got := shellArgFor(goos, path)
			assert.Equal(t, `"`+path+`"`, got, "%q on %s", path, goos)
		}
	}
}

// Everything a wrapping does neutralise is still wrapped, so the ordinary
// awkward name keeps a command that runs as printed.
func TestShellArgStillQuotesWhatQuotingFixes(t *testing.T) {
	cases := map[string]string{
		"./team evals":         `"./team evals"`,
		"./a;rm -rf b":         `"./a;rm -rf b"`,
		"a|b":                  `"a|b"`,
		"a&b":                  `"a&b"`,
		"a(b)":                 `"a(b)"`,
		`C:\Users\Me\My Evals`: `"C:\Users\Me\My Evals"`,
	}
	for in, want := range cases {
		assert.Equal(t, want, ShellArg(in), "%q is made safe by wrapping", in)
	}
}

// A value needing nothing is printed as itself, so the common command stays
// readable.
func TestShellArgLeavesAPlainValueAlone(t *testing.T) {
	assert.Equal(t, "./quality", ShellArg("./quality"))
	assert.Equal(t, `C:\Users\Me\quality`, ShellArg(`C:\Users\Me\quality`))
	assert.Equal(t, "an-eval", ShellArg("an-eval"))
	assert.Equal(t, `""`, ShellArg(""))
}

// The placeholder itself has to be inert: a reader who pastes without noticing
// gets a command that fails on the name, not one that runs something.
func TestThePlaceholderCarriesNoMetacharacter(t *testing.T) {
	assert.NotContains(t, shellArgNeedsQuoting, "$")
	assert.NotContains(t, shellArgNeedsQuoting, "`")
	assert.NotContains(t, shellArgNeedsQuoting, " ")
	assert.NotContains(t, shellArgNeedsQuoting, ";")
	assert.NotContains(t, shellArgNeedsQuoting, "<")
	assert.NotContains(t, shellArgNeedsQuoting, ">")
	assert.Equal(t, shellArgNeedsQuoting, ShellArg(shellArgNeedsQuoting),
		"and it survives its own rule, so it is not re-wrapped")
}
