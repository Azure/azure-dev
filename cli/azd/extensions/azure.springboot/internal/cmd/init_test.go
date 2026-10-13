// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitPreflight(t *testing.T) {
	for _, tt := range []struct {
		name      string
		args      []string
		input     string
		errorText string
	}{
		{"consent in scripts", []string{"init", "--no-prompt"}, "", "requires --yes"},
		{"ignored environment", []string{"init", "--yes", "--environment", "release"}, "", "--environment"},
		{"ignored output format", []string{"init", "--yes", "--output", "json"}, "", "--output"},
		{"no positional project", []string{"init", "somewhere"}, "", "unknown command"},
		{"decline trust", []string{"init"}, "n\n", "cancelled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("AZD_NO_PROMPT", "")
			t.Setenv("AZD_CWD", "")
			t.Setenv("AZD_ENVIRONMENT", "")
			command := NewRootCommand()
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetIn(strings.NewReader(tt.input))
			command.SetArgs(tt.args)
			err := command.ExecuteContext(t.Context())
			require.ErrorContains(t, err, tt.errorText)
			if tt.name == "decline trust" {
				require.Contains(t, output.String(), "Trust this project's Maven")
			}
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestConfirmationUsesInjectedWriter(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, confirm(&output, bufio.NewReader(strings.NewReader("yes\n")), "Proceed?"))
	require.Equal(t, "? Proceed? [y/N]: ", output.String())
	output.Reset()
	require.ErrorContains(t, confirm(&output, bufio.NewReader(strings.NewReader("no\n")), "Proceed?"), "cancelled")
	require.Equal(t, "? Proceed? [y/N]: ", output.String())
}
