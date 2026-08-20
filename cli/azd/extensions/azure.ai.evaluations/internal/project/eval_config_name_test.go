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

const minimalConfig = "datasets:\n  - name: golden\n    file: ./datasets/golden.jsonl\n"

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
		Datasets: []DatasetDecl{{Name: "added", File: "./datasets/added.jsonl"}},
	}))

	body, err := os.ReadFile(legacy)
	require.NoError(t, err)
	assert.Contains(t, string(body), "added")

	_, err = os.Stat(EvalConfigPath(dir))
	assert.True(t, os.IsNotExist(err),
		"a second configuration beside the one azure.yaml references would be inert")
}

// A directory holding both names is refused, not silently resolved. Preferring
// one is the dangerous answer: azure.yaml $refs a single file by name, so the
// CLI would edit one configuration while `azd up` deployed the other.
//
// This used to assert the preference instead, and `eval create` -- the one
// command that asks for the path rather than the parsed configuration -- read
// one of the two without a word while `run`, `init` and `generate` all refused.
func TestResolveEvalConfigPath_RefusesADirectoryHoldingBoth(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, LegacyEvalConfigBase), []byte(minimalConfig), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, EvalConfigBase), []byte(minimalConfig), 0o600))

	path, err := ResolveEvalConfigPath(dir)
	require.Error(t, err, "both names present has no single right answer")
	assert.Empty(t, path)
	assert.Contains(t, err.Error(), LegacyEvalConfigBase)
	assert.Contains(t, err.Error(), EvalConfigBase)
}

// The naming preference itself still holds for the callers that have already
// applied the guard: with only the legacy file there, that is the file a save
// writes back over.
func TestResolvedConfigPath_PrefersTheCurrentName(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, LegacyEvalConfigBase), []byte(minimalConfig), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, EvalConfigBase), []byte(minimalConfig), 0o600))

	assert.Equal(t, EvalConfigPath(dir), resolvedConfigPath(dir))
}

// An empty directory resolves to the current name, which is what a first
// generate creates.
func TestResolveEvalConfigPath_EmptyDirectoryUsesTheCurrentName(t *testing.T) {
	dir := t.TempDir()

	path, err := ResolveEvalConfigPath(dir)
	require.NoError(t, err)
	assert.Equal(t, EvalConfigPath(dir), path)
}

// Only the legacy file present resolves to it, so a project that has not
// migrated keeps working.
func TestResolveEvalConfigPath_LegacyOnlyResolvesToLegacy(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, LegacyEvalConfigBase), []byte(minimalConfig), 0o600))

	path, err := ResolveEvalConfigPath(dir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, LegacyEvalConfigBase), path)
}
