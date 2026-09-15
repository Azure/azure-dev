// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"azureaieval/internal/pkg/dataset_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingTree builds a root carrying the inherited -o/--output flag the SDK
// registers, with one command under it that fails.
func failingTree(err error) (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{Use: "eval"}
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

// A failing command under `-o json` used to write nothing to stdout, so a
// caller piping into a parser saw an empty stream and reported a syntax error
// of its own -- the reason for the failure never reached them.
func TestAJSONFailureIsReadableByTheCallerThatAskedForJSON(t *testing.T) {
	t.Parallel()

	root, out := failingTree(errors.New("the model deployment was not found"))
	root.SetArgs([]string{"boom", "-o", "json"})
	require.Error(t, root.Execute(), "the failure still fails")

	var doc struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc),
		"stdout under -o json has to parse as JSON: %q", out.String())
	assert.Equal(t, "the model deployment was not found", doc.Error.Message,
		"the reason has to survive into the document")
}

// The human surface is unchanged: azd renders the error itself, and a second
// copy on stdout would be noise.
func TestAHumanFailureDoesNotGainAJSONDocument(t *testing.T) {
	t.Parallel()

	root, out := failingTree(errors.New("nope"))
	root.SetArgs([]string{"boom"})
	require.Error(t, root.Execute())
	assert.Empty(t, out.String(), "azd prints the human line; this must not print a second one")
}

// A command that succeeds is left exactly as it was.
func TestASuccessIsNotTurnedIntoAnError(t *testing.T) {
	t.Parallel()

	root := &cobra.Command{Use: "eval"}
	root.PersistentFlags().StringP("output", "o", "", "")
	root.AddCommand(&cobra.Command{
		Use: "fine",
		RunE: func(c *cobra.Command, _ []string) error {
			return emitJSON(c.OutOrStdout(), map[string]string{"status": "ok"})
		},
	})
	reportFailuresAsJSON(root)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"fine", "-o", "json"})
	require.NoError(t, root.Execute())
	assert.NotContains(t, out.String(), "error", "a success carries no error key")
}

// The real tree, not a stand-in: the wrapping is wired in NewRootCommand, and a
// test that assembles its own tree passes whether or not that wiring is there.
//
// `dataset create` rejects the name before it opens a connection, so this is a
// failure the test can reach without a project or an endpoint.
func TestTheShippedTreeAnswersAFailureAsJSON(t *testing.T) {
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{
		"dataset", "create", "not a valid name", "--from-file", "nothing.jsonl", "-o", "json",
	})
	require.Error(t, root.Execute())

	var doc struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc),
		"stdout under -o json has to parse as JSON: %q", out.String())
	assert.NotEmpty(t, doc.Error.Message, "the document has to carry the reason")
}

// `run` writes the run and only then decides whether the status it carries is a
// failure. Appending an error document there leaves two values on stdout, which
// jq tolerates and json.loads and JSON.parse do not -- so the caller keeps the
// document they were given, and the reason stays on stderr.
func TestAFailureAfterTheDocumentDoesNotAppendASecond(t *testing.T) {
	t.Parallel()

	root := &cobra.Command{Use: "eval"}
	root.PersistentFlags().StringP("output", "o", "", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.AddCommand(&cobra.Command{
		Use: "run",
		RunE: func(c *cobra.Command, _ []string) error {
			if err := emitJSON(c.OutOrStdout(), map[string]string{"id": "run-1", "status": "failed"}); err != nil {
				return err
			}
			return errors.New("run did not complete")
		},
	})
	reportFailuresAsJSON(root)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"run", "-o", "json"})
	require.Error(t, root.Execute())

	dec := json.NewDecoder(bytes.NewReader(out.Bytes()))
	var first map[string]any
	require.NoError(t, dec.Decode(&first), "the run document has to survive")
	assert.Equal(t, "run-1", first["id"])

	var second map[string]any
	assert.ErrorIs(t, dec.Decode(&second), io.EOF,
		"stdout carried a second document: %q", out.String())
}

// A misspelled flag stops pflag where it stands, so a `-o json` written after
// it was never parsed and the caller who asked for a document got prose. The
// answer must not depend on where on the line the caller put the flag.
func TestTheRequestedFormatIsFoundWhereverItSitsOnTheLine(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"before an unknown flag", []string{"-o", "json", "--typo"}, "json"},
		{"after an unknown flag", []string{"--typo", "-o", "json"}, "json"},
		{"after a bad value for a real flag", []string{"--limit", "nope", "-o", "json"}, "json"},
		{"long spelling, attached", []string{"--typo", "--output=json"}, "json"},
		{"among positional arguments", []string{"create", "name", "-o", "json"}, "json"},
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
// goes to stderr.
//
// `run --gate-on-status` reaches this: it emits the run and only then decides
// that the status it carries is a failure. Neither a second document nor the
// prose azd would otherwise put on stdout can join the first one without
// leaving the stream unparseable, so the reason goes beside it instead.
func TestAReasonAfterTheDocumentGoesBesideItNotIntoIt(t *testing.T) {
	t.Parallel()

	root := &cobra.Command{Use: "eval"}
	root.PersistentFlags().StringP("output", "o", "", "")
	root.SilenceErrors = true
	root.SilenceUsage = true
	root.AddCommand(&cobra.Command{
		Use: "gated",
		RunE: func(c *cobra.Command, _ []string) error {
			if err := emitJSON(c.OutOrStdout(), map[string]string{"id": "run_1", "status": "failed"}); err != nil {
				return err
			}
			return errors.New("the run did not complete")
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
	assert.Equal(t, "run_1", doc["id"], "the document the command wrote is the one that survives")
	assert.Contains(t, errOut.String(), "the run did not complete",
		"the reason still has to reach the caller, just not on stdout")
}

// Tags are what a generated dataset says which job produced it with, and the
// standalone `azd ai dataset show` prints them. A reader moving between the two
// surfaces should not have to learn which one hides what the other shows.
func TestDatasetShowPrintsTheTagsTheStandaloneSurfaceShows(t *testing.T) {
	t.Parallel()

	keys := func(fields []field) []string {
		out := make([]string, 0, len(fields))
		for _, f := range fields {
			out = append(out, f.Key)
		}
		return out
	}

	tagged := datasetShowFields(&dataset_api.Dataset{
		Name: "golden", Version: "1.0", Type: "uri_file",
		Tags: map[string]string{"generated-by": "nightly"},
	})
	assert.Contains(t, keys(tagged), "Tags", "a tagged version has to say so")

	untagged := datasetShowFields(&dataset_api.Dataset{
		Name: "golden", Version: "1.0", Type: "uri_file",
	})
	assert.NotContains(t, keys(untagged), "Tags",
		"an untagged version gains no empty row, the rule the TAGS column follows")
}
