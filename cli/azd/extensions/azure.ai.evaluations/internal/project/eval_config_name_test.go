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

const minimalConfig = "datasets:\n  - name: golden\n    source: ./datasets/golden.jsonl\n"

// The file is named for azd, the way azure.yaml is.
func TestEvalConfigPath_IsTheAzdPrefixedName(t *testing.T) {
	assert.Equal(t, filepath.Join("evals", "azure.eval.yaml"), EvalConfigPath("evals"))
}

// A project written before the rename has a checked-in eval.yaml and an
// azure.yaml $ref pointing at it. Reading has to find it, or every such project
// silently looks empty.
func TestOpenEvalConfig_ReadsALegacyFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, LegacyEvalConfigBase), []byte(minimalConfig), 0o600))

	cfg, err := OpenEvalConfig(dir)

	require.NoError(t, err)
	require.NotNil(t, cfg, "a legacy configuration is still a configuration")
	require.Len(t, cfg.Datasets, 1)
	assert.Equal(t, "golden", cfg.Datasets[0].Name)
}

// Writing back into such a project has to update the file it already
// references. Creating azure.eval.yaml beside it would leave the one azure.yaml
// $refs untouched, so the entry would be invisible to azd up.
func TestSaveEvalConfig_WritesBackOverALegacyFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, LegacyEvalConfigBase)
	require.NoError(t, os.WriteFile(legacy, []byte(minimalConfig), 0o600))

	require.NoError(t, SaveEvalConfig(dir, &EvalConfig{
		Datasets: []DatasetDecl{{Name: "added", Source: "./datasets/added.jsonl"}},
	}))

	body, err := os.ReadFile(legacy)
	require.NoError(t, err)
	assert.Contains(t, string(body), "added")

	_, err = os.Stat(EvalConfigPath(dir))
	assert.True(t, os.IsNotExist(err),
		"a second configuration beside the one azure.yaml references would be inert")
}

// With both present the current name wins, so a project that has migrated is
// not dragged back to the old file.
func TestResolveEvalConfigPath_PrefersTheCurrentName(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, LegacyEvalConfigBase), []byte(minimalConfig), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, EvalConfigBase), []byte(minimalConfig), 0o600))

	assert.Equal(t, EvalConfigPath(dir), ResolveEvalConfigPath(dir))
}

// An empty directory resolves to the current name, which is what a first
// generate creates.
func TestResolveEvalConfigPath_EmptyDirectoryUsesTheCurrentName(t *testing.T) {
	dir := t.TempDir()

	assert.Equal(t, EvalConfigPath(dir), ResolveEvalConfigPath(dir))
}
