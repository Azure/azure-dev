// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaidataset/internal/messages"
	"azureaidataset/internal/pkg/dataset_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// commandWithOutput builds a command carrying the -o flag the azd SDK root
// supplies at runtime, on the same flag set the production code reads.
func commandWithOutput(t *testing.T, value string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().StringP("output", "o", "", "")
	require.NoError(t, cmd.Flags().Set("output", value))
	cmd.SetOut(&bytes.Buffer{})
	return cmd
}

// The flag selects machine-readable output, and a caller who types JSON in
// caps means the same thing as one who does not.
func TestOutputFormatAndIsJSON(t *testing.T) {
	assert.True(t, isJSON(commandWithOutput(t, "json")))
	assert.True(t, isJSON(commandWithOutput(t, "JSON")), "the format is matched without regard to case")
	assert.False(t, isJSON(commandWithOutput(t, "table")))
	assert.False(t, isJSON(commandWithOutput(t, "")))

	assert.False(t, isJSON(nil), "a command with no flags is not JSON output")
	assert.Empty(t, outputFormat(nil))

	// A command that never declared -o must not panic on being asked.
	assert.Empty(t, outputFormat(&cobra.Command{Use: "bare"}))
}

// A nil slice encodes as null, which a caller iterating the result reads as a
// type error rather than as an empty list.
func TestEmitJSONListNormalizesNil(t *testing.T) {
	var buf bytes.Buffer
	var none []dataset_api.Dataset
	require.NoError(t, emitJSONList(&buf, none))
	assert.Equal(t, "[]", strings.TrimSpace(buf.String()))

	buf.Reset()
	require.NoError(t, emitJSONList(&buf, []dataset_api.Dataset{{Name: "a", Version: "1.0"}}))
	var round []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &round))
	require.Len(t, round, 1)
	assert.Equal(t, "a", round[0]["name"])
}

// The list emits a bare array, not the envelope the service replied with: the
// envelopes disagree with each other and carry paging this extension does not
// follow.
func TestEmitJSONListDropsTheEnvelope(t *testing.T) {
	cmd := commandWithOutput(t, "json")
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, renderDatasets(cmd, &dataset_api.DatasetList{
		Value:    []dataset_api.Dataset{{Name: "a", Version: "1.0"}},
		NextLink: "https://example/page2",
	}, messages.NoDatasets()))

	assert.True(t, strings.HasPrefix(strings.TrimSpace(buf.String()), "["),
		"a list answers with an array")
	assert.NotContains(t, buf.String(), "nextLink",
		"paging this extension does not follow must not suggest there is more to fetch")
}

// A list view is uppercase headers over a rule. The rule is what separates the
// header from the data at a glance, and it is what the sibling extensions print.
func TestRenderDatasetsTable(t *testing.T) {
	cmd := commandWithOutput(t, "")
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, renderDatasets(cmd, &dataset_api.DatasetList{Value: []dataset_api.Dataset{
		{Name: "golden", Version: "2.0", Type: "uri_file"},
		{Name: "smoke", Version: "1.0", Type: "uri_file"},
	}}, messages.NoDatasets()))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 4, "a header, its rule, and one line per dataset")
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "VERSION")
	// The API accepts format on upload and never returns it, so a FORMAT column
	// was blank for every dataset. Type is what the service actually sends.
	assert.Contains(t, lines[0], "TYPE")
	assert.True(t, strings.HasPrefix(strings.TrimSpace(lines[1]), "----"),
		"the rule under the header is the convention, got %q", lines[1])
	assert.Contains(t, lines[2], "golden")
	assert.Contains(t, lines[2], "uri_file")
	assert.Contains(t, lines[3], "smoke")
}

// An empty project has to say so. A bare header over nothing reads as output
// that got cut off.
func TestRenderDatasetsSaysWhenThereAreNone(t *testing.T) {
	cmd := commandWithOutput(t, "")
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, renderDatasets(cmd, &dataset_api.DatasetList{}, messages.NoDatasets()))
	assert.Contains(t, buf.String(), "No datasets found.")
	assert.NotContains(t, buf.String(), "NAME")
}

// Listing one name's versions and listing every dataset share a renderer but
// not a question. "No datasets found." in a project holding a dozen datasets
// reads as though the lookup failed rather than as an answer about that name.
func TestRenderDatasetsSaysWhichNameHasNoVersions(t *testing.T) {
	cmd := commandWithOutput(t, "")
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, renderDatasets(cmd, &dataset_api.DatasetList{},
		messages.NoDatasetVersions("golden")))

	assert.Contains(t, buf.String(), `No versions of dataset "golden"`)
	assert.NotContains(t, buf.String(), "No datasets found.")
	assert.NotContains(t, buf.String(), "NAME")
}

// The empty result is still a success with an empty array: a delete is checked
// for idempotence by listing what is left, and `-o json` callers range over it.
func TestVersionsListEmptyIsStillAnEmptyJSONArray(t *testing.T) {
	cmd := commandWithOutput(t, "json")
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	require.NoError(t, renderDatasets(cmd, &dataset_api.DatasetList{},
		messages.NoDatasetVersions("golden")))

	assert.Equal(t, "[]", strings.TrimSpace(buf.String()),
		"the sentence is for a reader; a parser still gets an array")
}

// A detail view is Title Case key/value, the shape `show` uses, and a blank
// value is dropped rather than printed as an empty column.
func TestEmitDetail(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitDetail(&buf, []field{
		{"Name", "golden"},
		{"Version", "2.0"},
		{"Description", ""},
		{"Format", "jsonl"},
	}))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3, "the empty Description is dropped")
	assert.True(t, strings.HasPrefix(lines[0], "Name"))
	assert.Contains(t, lines[0], "golden")
	assert.NotContains(t, buf.String(), "Description")
}

// `show` returns one thing, so the spec's output conventions make it a detail
// view rather than the raw JSON it would otherwise be easiest to print.
func TestShowUsesADetailView(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(".", "dataset.go"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "emitDetail",
		"dataset show returns one thing, so dataset.go renders it as a detail view")
}

// The message has to name the flag that would have supplied the value, and
// nothing else: none of these commands prompts, so blaming --no-prompt named a
// flag the caller had not passed and implied that dropping it would make the
// command ask.
func TestRequireFlag(t *testing.T) {
	err := requireFlag("name")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--name")
	assert.NotContains(t, err.Error(), "--no-prompt")
}

// --from-file takes either the file or the directory holding it, because both
// are what a caller has to hand. Anything else is worth refusing by name.
func TestDatasetUploadSource(t *testing.T) {
	dir := t.TempDir()
	rows := filepath.Join(dir, "rows.jsonl")
	require.NoError(t, os.WriteFile(rows, []byte("{\"query\":\"q\"}\n"), 0o600))

	got, err := datasetUploadSource(rows)
	require.NoError(t, err)
	assert.Equal(t, rows, got,
		"a named file is uploaded, not whichever .jsonl its directory sorts first")

	got, err = datasetUploadSource(dir)
	require.NoError(t, err)
	assert.Equal(t, rows, got,
		"a directory resolves to the one .jsonl in it, named rather than scanned later")

	notJSONL := filepath.Join(dir, "rows.csv")
	require.NoError(t, os.WriteFile(notJSONL, []byte("a,b\n"), 0o600))
	_, err = datasetUploadSource(notJSONL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".jsonl")

	_, err = datasetUploadSource(filepath.Join(dir, "missing.jsonl"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from-file")
}
