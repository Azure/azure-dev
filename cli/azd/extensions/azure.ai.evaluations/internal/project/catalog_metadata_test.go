// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readCatalog returns the written configuration as text.
func readCatalog(t *testing.T, dir string) string {
	t.Helper()
	path, err := ResolveEvalConfigPath(dir)
	require.NoError(t, err)
	// #nosec G304 -- the path is inside this test's own TempDir.
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(body)
}

// A generated evaluator carries catalog metadata the service assigned. Writing
// only `source:` threw it away, and the next `azd up` republished the evaluator
// with a blank catalog name and whatever compatibility the service inferred --
// narrower than the version before it.
func TestAnEvaluatorEntryKeepsItsCatalogMetadata(t *testing.T) {
	dir := t.TempDir()

	changed, created, err := UpsertCatalogFields(dir, "evaluators", "hero-evaluator",
		[]CatalogField{
			{Key: "source", Value: "./evaluators/hero-evaluator.json"},
			{Key: "display_name", Value: "hero-evaluator"},
			{Key: "categories", List: []string{"quality", "agents"}},
			{Key: "supported_evaluation_levels", List: []string{"turn", "conversation"}},
		})

	require.NoError(t, err)
	assert.True(t, changed)
	assert.True(t, created)

	text := readCatalog(t, dir)
	assert.Contains(t, text, "display_name: hero-evaluator")
	assert.Contains(t, text, "- quality")
	assert.Contains(t, text, "- agents")
	assert.Contains(t, text, "supported_evaluation_levels:")
	assert.Contains(t, text, "- conversation",
		"the complete returned list, not the level of the first referencing eval")

	// And it reads back through the typed model, which is what `azd up` uses.
	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "hero-evaluator", cfg.Evaluators[0].DisplayName)
	assert.Equal(t, []string{"quality", "agents"}, cfg.Evaluators[0].Categories)
	assert.Equal(t, []string{"turn", "conversation"},
		cfg.Evaluators[0].SupportedEvaluationLevels)
}

// Turn rows and conversation seeds are different things, and nothing in the
// file said which a generated dataset held -- so reconciliation and filtering
// had to open it to find out.
func TestAGeneratedDatasetRecordsItsEvaluationLevel(t *testing.T) {
	dir := t.TempDir()

	_, _, err := UpsertCatalogFields(dir, "datasets", "turn-tests", []CatalogField{
		{Key: "file", Value: "./datasets/turn-tests.jsonl"},
		{Key: "tags.evaluation_level", Value: "turn"},
	})
	require.NoError(t, err)

	assert.Contains(t, readCatalog(t, dir), "evaluation_level: turn")

	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Datasets, 1)
	assert.Equal(t, "turn", cfg.Datasets[0].Tags["evaluation_level"])
}

// A field the service did not return is omitted rather than written blank. An
// empty `categories:` in the file is a claim that the evaluator has none, and
// republishing would carry it.
func TestAnAbsentFieldIsNotWrittenBlank(t *testing.T) {
	dir := t.TempDir()

	_, _, err := UpsertCatalogFields(dir, "evaluators", "plain", []CatalogField{
		{Key: "source", Value: "./evaluators/plain.json"},
		{Key: "display_name", Value: "plain"},
		{Key: "categories", List: nil},
		{Key: "supported_evaluation_levels", List: nil},
	})
	require.NoError(t, err)

	text := readCatalog(t, dir)
	assert.NotContains(t, text, "categories")
	assert.NotContains(t, text, "supported_evaluation_levels")
}

// Regenerating writes the same values, and rewriting the file to change nothing
// would still rewrite it -- past the author's comments and formatting.
func TestRewritingTheSameMetadataChangesNothing(t *testing.T) {
	dir := t.TempDir()
	fields := []CatalogField{
		{Key: "source", Value: "./evaluators/hero.json"},
		{Key: "display_name", Value: "hero"},
		{Key: "categories", List: []string{"quality"}},
	}

	_, _, err := UpsertCatalogFields(dir, "evaluators", "hero", fields)
	require.NoError(t, err)
	before, err := os.Stat(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err)

	changed, _, err := UpsertCatalogFields(dir, "evaluators", "hero", fields)
	require.NoError(t, err)

	assert.False(t, changed, "the entry already says all of this")
	after, err := os.Stat(filepath.Join(dir, EvalConfigBase))
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime(), "and the file was left alone")
}
