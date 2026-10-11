// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Double quotes are the only wrapping cmd, PowerShell, bash and zsh all read
// the same way, but they do not make a value literal: $ and backticks still
// expand inside them, and \" does not escape a quote in PowerShell. These
// values come out of the configuration file, so a printed step carrying one
// would run it when pasted.
func TestShellArgRefusesToInlineWhatItCannotMakeLiteral(t *testing.T) {
	for _, v := range []string{
		"./eval$dir",
		"$(whoami)",
		"a`whoami`b",
		`say "hi"`,
		"${HOME}",
	} {
		assert.Equal(t, "VALUE_NEEDS_QUOTING", ShellArg(v),
			"%q expands or breaks the quoting, so it must not be inlined", v)
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
