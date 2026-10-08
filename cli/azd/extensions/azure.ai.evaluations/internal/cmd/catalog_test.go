// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/project"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// catalogCommand builds a command whose output can be read back.
func catalogCommand(t *testing.T, buf *bytes.Buffer) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "generate"}
	cmd.Flags().StringP("output", "o", "", "")
	cmd.SetOut(buf)
	return cmd
}

// The spec's Scenario 2 transcript names what was recorded and the version it
// was published at. "Added catalog entry" says neither, which leaves the reader
// to infer both from the line above it.
func TestCatalogLineNamesTheArtifactAndVersion(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer

	require.NoError(t, addDatasetToCatalog(catalogCommand(t, &buf), dir, &project.ArtifactRef{
		Name:    "support-agent-regression",
		Source:  "datasets/support-agent-regression.jsonl",
		Version: "1",
	}))

	out := buf.String()
	assert.Contains(t, out, `Added dataset 'support-agent-regression' (version 1) to`)
	assert.Contains(t, out, "eval.yaml")
}

// The evaluator half of the same transcript.
func TestCatalogLineForAnEvaluator(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer

	require.NoError(t, addEvaluatorToCatalog(catalogCommand(t, &buf), dir, &project.ArtifactRef{
		Name:    "support-agent-quality",
		Source:  "evaluators/support-agent-quality.json",
		Version: "1",
	}))

	assert.Contains(t, buf.String(), `Added evaluator 'support-agent-quality' (version 1) to`)
}

// A job that reported no version still has to name what it recorded, rather
// than printing an empty parenthesis or the word "latest" as if it were one.
func TestCatalogLineWithoutAVersion(t *testing.T) {
	for _, version := range []string{"", "latest"} {
		dir := t.TempDir()
		var buf bytes.Buffer

		require.NoError(t, addDatasetToCatalog(catalogCommand(t, &buf), dir, &project.ArtifactRef{
			Name: "golden", Source: "datasets/golden.jsonl", Version: version,
		}))

		out := buf.String()
		assert.Contains(t, out, `Added dataset 'golden' to`)
		assert.NotContains(t, out, "version", "version %q is not one to print", version)
	}
}

// The first generate in a repository has no configuration to append to, so it
// says the file was created as well as what went into it.
func TestCatalogLineWhenTheFileIsCreated(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer

	require.NoError(t, addDatasetToCatalog(catalogCommand(t, &buf), dir, &project.ArtifactRef{
		Name: "golden", Source: "datasets/golden.jsonl", Version: "1",
	}))

	out := buf.String()
	assert.Contains(t, out, "Created")
	assert.Contains(t, out, `Added dataset 'golden' (version 1) to`,
		"creating the file still has to say what was put in it")

	// The entry is really on disk, not just announced.
	body, err := os.ReadFile(filepath.Join(dir, project.EvalConfigBase))
	require.NoError(t, err)
	assert.Contains(t, string(body), "golden")
}

// Regenerating the same artifact to the same path changes nothing, so it must
// not claim it did.
func TestCatalogSaysNothingWhenNothingChanged(t *testing.T) {
	dir := t.TempDir()
	ref := &project.ArtifactRef{Name: "golden", Source: "datasets/golden.jsonl", Version: "1"}

	var first bytes.Buffer
	require.NoError(t, addDatasetToCatalog(catalogCommand(t, &first), dir, ref))
	require.Contains(t, first.String(), "Added dataset")

	var second bytes.Buffer
	require.NoError(t, addDatasetToCatalog(catalogCommand(t, &second), dir, ref))
	assert.Empty(t, second.String(), "an unchanged catalog is not an edit")
}
