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
// PowerShell, so a value with nothing that any of them reads specially inside
// them is printed as itself, single-quoted, rather than as a placeholder
// nobody can run.
func TestShellArgSingleQuotesWhatDoubleQuotesCannotMakeLiteral(t *testing.T) {
	for _, tc := range []struct {
		in, posix, windows string
	}{
		{"./eval$dir", `'./eval$dir'`, `'./eval$dir'`},
		{"$(whoami)", `'$(whoami)'`, `'$(whoami)'`},
		{"a`whoami`b", "'a`whoami`b'", "'a`whoami`b'"},
		{"${HOME}", `'${HOME}'`, `'${HOME}'`},
		{"/home/me/my evals/$x/a.yaml", `'/home/me/my evals/$x/a.yaml'`, `'/home/me/my evals/$x/a.yaml'`},
	} {
		assert.Equal(t, tc.posix, shellArgFor("linux", tc.in), "%q on a POSIX shell", tc.in)
		assert.Equal(t, tc.posix, shellArgFor("darwin", tc.in), "%q on macOS", tc.in)
		assert.Equal(t, tc.windows, shellArgFor("windows", tc.in), "%q on Windows", tc.in)
	}
	// Windows paths keep their backslashes in PowerShell, and an ASCII ' is
	// doubled there.
	assert.Equal(t, `'C:\Users\Me\My $dir\a.yaml'`, shellArgFor("windows", `C:\Users\Me\My $dir\a.yaml`))
	assert.Equal(t, `'it''s $x'`, shellArgFor("windows", "it's $x"))
}

// What cannot be both runnable and safe is named rather than inlined, and the
// placeholder is the only thing printed for it. On Windows cmd.exe does not read
// single quotes, so a value cmd would act on is not printed, and a double quote
// flips the quoting cmd keeps for the arguments after it. Off Windows an ASCII '
// has no escape that bash, zsh and PowerShell all read the same way, and fish
// reads \' and \\ inside single quotes, so a quote or a backslash is not printed
// beside a $, a backtick or a double quote. PowerShell reads the typographic
// single quotes as quotes on every operating system. A control character has no
// quoting anywhere.
func TestShellArgStillRefusesWhatNoQuotingMakesSafe(t *testing.T) {
	for _, v := range []string{"$x&calc", "$x|calc", "$x>out", "$x^y", "%PATH%$x", "a$\nb", `x"y$`, `say "hi"`} {
		assert.Equal(t, "VALUE_NEEDS_QUOTING", shellArgFor("windows", v),
			"%q would be acted on by cmd.exe inside single quotes", v)
	}
	for _, v := range []string{"it's $x", `$x';calc;'`, `a\b$x`, `$x\`, `\';calc;\'$`, `say "hi" it's`} {
		for _, goos := range []string{"linux", "darwin"} {
			assert.Equal(t, "VALUE_NEEDS_QUOTING", shellArgFor(goos, v),
				"%q has no single escape every POSIX shell and PowerShell read alike on %s", v, goos)
		}
	}
	for _, v := range []string{"a$x\u2019; calc; \u2018b", "\u2018$x", "$x\u201a", "$x\u201b"} {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			assert.Equal(t, "VALUE_NEEDS_QUOTING", shellArgFor(goos, v),
				"%q holds a character PowerShell reads as a single quote on %s", v, goos)
		}
	}
	for _, v := range []string{"a\x00b", "a\x1bb", "a\rb"} {
		for _, goos := range []string{"linux", "windows"} {
			assert.Equal(t, "VALUE_NEEDS_QUOTING", shellArgFor(goos, v), "%q has no quoting on %s", v, goos)
		}
	}
	// POSIX single quotes are literal for those same characters.
	assert.Equal(t, `'$x&calc'`, shellArgFor("linux", "$x&calc"))
	assert.Equal(t, "'a$\nb'", shellArgFor("linux", "a$\nb"))
}

// Nothing the placeholder stands in for comes through as a quoted value: what
// is printed either carries the value whole between single quotes or is the
// placeholder, never a half-quoted form that a shell would read differently.
func TestAPrintedValueIsEitherQuotedWholeOrThePlaceholder(t *testing.T) {
	values := []string{
		`C:\Users\Me\run$1\azure.eval.yaml`,
		`/tmp/it's a "test"/azure.eval.yaml`,
		"$x';Write-Output INJECTED;'",
		"a$x\u2019; Write-Output INJECTED; \u2018b",
		`x"y$`,
		`\';calc;\'$`,
	}
	for _, v := range values {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			got := shellArgFor(goos, v)
			if got == "VALUE_NEEDS_QUOTING" {
				continue
			}
			assert.True(t, strings.HasPrefix(got, "'") && strings.HasSuffix(got, "'"), "%q on %s: %s", v, goos, got)
			assert.Equal(t, v, strings.ReplaceAll(got[1:len(got)-1], "''", "'"), "%q on %s is carried whole", v, goos)
			unpaired := strings.ReplaceAll(got[1:len(got)-1], "''", "")
			assert.NotContains(t, unpaired, "'", "%q on %s keeps its quoting balanced", v, goos)
		}
	}
}

// The command a caller prints must run as printed, so a path with spaces or a
// dollar sign is the real path and never the placeholder on any shell.
func TestAPrintedPathIsTheRealPathNeverThePlaceholder(t *testing.T) {
	for _, path := range []string{
		`C:\Users\Me\My Evals\azure.eval.yaml`,
		`C:\Users\Me\run$1\azure.eval.yaml`,
		"/home/me/my evals/run$1/azure.eval.yaml",
	} {
		goses := []string{"linux", "darwin", "windows"}
		if strings.Contains(path, `\`) {
			goses = []string{"windows"}
		}
		for _, goos := range goses {
			got := shellArgFor(goos, path)
			assert.NotEqual(t, "VALUE_NEEDS_QUOTING", got, "%q on %s", path, goos)
			assert.Contains(t, got, path, "the path is carried whole")
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
