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
// PowerShell, so those values are printed as themselves, single-quoted, rather
// than as a placeholder nobody can run.
func TestShellArgSingleQuotesWhatDoubleQuotesCannotMakeLiteral(t *testing.T) {
	for _, tc := range []struct {
		in, posix, windows string
	}{
		{"./eval$dir", `'./eval$dir'`, `'./eval$dir'`},
		{"$(whoami)", `'$(whoami)'`, `'$(whoami)'`},
		{"a`whoami`b", "'a`whoami`b'", "'a`whoami`b'"},
		{`say "hi"`, `'say "hi"'`, `'say "hi"'`},
		{"${HOME}", `'${HOME}'`, `'${HOME}'`},
		{`C:\Users\Me\My $dir\a.yaml`, `'C:\Users\Me\My $dir\a.yaml'`, `'C:\Users\Me\My $dir\a.yaml'`},
		{"/home/me/my evals/$x/a.yaml", `'/home/me/my evals/$x/a.yaml'`, `'/home/me/my evals/$x/a.yaml'`},
		{"it's $x", "'it'\\''s $x'", "'it''s $x'"},
	} {
		assert.Equal(t, tc.posix, shellArgFor("linux", tc.in), "%q on a POSIX shell", tc.in)
		assert.Equal(t, tc.posix, shellArgFor("darwin", tc.in), "%q on macOS", tc.in)
		assert.Equal(t, tc.windows, shellArgFor("windows", tc.in), "%q on Windows", tc.in)
	}
}

// What cannot be both runnable and safe is still named rather than inlined: on
// Windows cmd.exe does not read single quotes, so a value cmd would act on is
// not printed, and a control character has no quoting anywhere.
func TestShellArgStillRefusesWhatNoQuotingMakesSafe(t *testing.T) {
	for _, v := range []string{"$x&calc", "$x|calc", "$x>out", "$x^y", "%PATH%$x", "a$\nb"} {
		assert.Equal(t, "VALUE_NEEDS_QUOTING", shellArgFor("windows", v),
			"%q would be acted on by cmd.exe inside single quotes", v)
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

// The command a caller prints must run as printed, so the printed value is the
// real one and never the placeholder, for a path with spaces, a dollar sign and a
// quote on either family of shell.
func TestAPrintedPathIsTheRealPathNeverThePlaceholder(t *testing.T) {
	for _, path := range []string{
		`C:\Users\Me\My Evals\azure.eval.yaml`,
		`C:\Users\Me\run$1\azure.eval.yaml`,
		`/tmp/it's a "test"/azure.eval.yaml`,
	} {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			got := shellArgFor(goos, path)
			assert.NotEqual(t, "VALUE_NEEDS_QUOTING", got, "%q on %s", path, goos)
			if !strings.Contains(path, "'") {
				assert.Contains(t, got, path, "the path is carried whole")
			}
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
