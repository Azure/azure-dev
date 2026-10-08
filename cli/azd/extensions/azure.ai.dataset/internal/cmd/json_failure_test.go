// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingTree builds a root carrying the inherited -o/--output flag the SDK
// registers, with one command under it that fails.
func failingTree(err error) (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{Use: "dataset"}
	root.PersistentFlags().StringP("output", "o", "", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.AddCommand(&cobra.Command{
		Use:  "boom",
		RunE: func(*cobra.Command, []string) error { return err },
	})
	reportFailuresAsJSON(root)

	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	return root, out
}

// A failing command under `-o json` wrote nothing to stdout, so a caller piping
// into a parser saw an empty stream and reported a syntax error of its own --
// the reason never reached the thing that needed it.
func TestAJSONFailureIsReadableByTheCallerThatAskedForJSON(t *testing.T) {
	t.Parallel()

	root, out := failingTree(errors.New("the dataset was not found"))
	root.SetArgs([]string{"boom", "-o", "json"})
	require.Error(t, root.Execute())

	var doc struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc),
		"stdout under -o json has to parse as JSON: %q", out.String())
	assert.Equal(t, "the dataset was not found", doc.Error.Message)
}

// The human surface is unchanged: azd renders the error itself, and a second
// copy on stdout would be noise.
func TestAHumanFailureDoesNotGainAJSONDocument(t *testing.T) {
	t.Parallel()

	root, out := failingTree(errors.New("nope"))
	root.SetArgs([]string{"boom"})
	require.Error(t, root.Execute())
	assert.Empty(t, out.String())
}

// The real tree, not a stand-in: the wrapping is wired in NewRootCommand, and a
// test that assembles its own tree passes whether or not that wiring is there.
func TestTheShippedTreeAnswersAFailureAsJSON(t *testing.T) {
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"show", "a", "b", "-o", "json"})
	require.Error(t, root.Execute())

	var doc struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc),
		"stdout under -o json has to parse as JSON: %q", out.String())
	assert.NotEmpty(t, doc.Error.Message)
}

// A misspelled flag stops pflag where it stands, so a `-o json` written after it
// was never parsed. The answer must not depend on where on the line it sits.
func TestTheRequestedFormatIsFoundWhereverItSitsOnTheLine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"before an unknown flag", []string{"-o", "json", "--typo"}, "json"},
		{"after an unknown flag", []string{"--typo", "-o", "json"}, "json"},
		{"long spelling, attached", []string{"--typo", "--output=json"}, "json"},
		{"not asked for at all", []string{"--typo", "create"}, ""},
		{"asked for something else", []string{"-o", "table", "--typo"}, "table"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, outputFromRawArgs(tc.args))
		})
	}
}

// A command that answered and then failed keeps its document, and the reason
// goes to stderr rather than joining it on stdout.
func TestAReasonAfterTheDocumentGoesBesideItNotIntoIt(t *testing.T) {
	t.Parallel()

	root := &cobra.Command{Use: "dataset"}
	root.PersistentFlags().StringP("output", "o", "", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.AddCommand(&cobra.Command{
		Use: "gated",
		RunE: func(c *cobra.Command, _ []string) error {
			if err := emitJSON(c.OutOrStdout(), map[string]string{"name": "golden"}); err != nil {
				return err
			}
			return errors.New("the upload did not complete")
		},
	})
	reportFailuresAsJSON(root)

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"gated", "-o", "json"})
	require.Error(t, root.Execute())

	var doc map[string]string
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc),
		"stdout has to stay one parseable document: %q", out.String())
	assert.Equal(t, "golden", doc["name"])
	assert.Contains(t, errOut.String(), "the upload did not complete")
}
