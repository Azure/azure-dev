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

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600))
}

const oneEvalConfig = "datasets:\n  - name: d\n    file: ./d.jsonl\n"

// azure.yaml references one configuration by name. With both files present the
// CLI would edit whichever it preferred while azd up deployed whichever the
// $ref named, and nothing would say so.
func TestOpenEvalConfig_RefusesBothNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, EvalConfigBase, oneEvalConfig)
	writeFile(t, dir, LegacyEvalConfigBase, oneEvalConfig)

	_, err := OpenEvalConfig(dir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), EvalConfigBase)
	assert.Contains(t, err.Error(), LegacyEvalConfigBase)
}

// The same refusal has to apply on the way out, or generate would append a
// catalog entry to one file while the deployment read the other.
func TestSaveEvalConfig_RefusesBothNames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, EvalConfigBase, oneEvalConfig)
	writeFile(t, dir, LegacyEvalConfigBase, oneEvalConfig)

	err := SaveEvalConfig(dir, &EvalConfig{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "only one")
}

// A project that predates the rename keeps working, and must not silently grow
// a second configuration beside the one it already has.
func TestSaveEvalConfig_WritesBackToTheLegacyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, LegacyEvalConfigBase, oneEvalConfig)

	require.NoError(t, SaveEvalConfig(dir, &EvalConfig{
		Datasets: []DatasetDecl{{Name: "d", File: "./d.jsonl"}},
	}))

	assert.FileExists(t, filepath.Join(dir, LegacyEvalConfigBase))
	assert.NoFileExists(t, filepath.Join(dir, EvalConfigBase),
		"a legacy project must not grow a second configuration")
}

// A fresh directory gets the current name.
func TestSaveEvalConfig_WritesTheCurrentNameWhenThereIsNoFile(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, SaveEvalConfig(dir, &EvalConfig{}))

	assert.FileExists(t, filepath.Join(dir, EvalConfigBase))
	assert.NoFileExists(t, filepath.Join(dir, LegacyEvalConfigBase))
}

// A target with no name was scored as though nothing were invoked, which is a
// different evaluation from the one that was written down.
func TestValidate_RefusesATargetWithNoName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, EvalConfigBase,
		"datasets:\n  - name: d\n    file: ./d.jsonl\n"+
			"evals:\n  - name: e\n    dataset: d\n"+
			"    target:\n      type: agent\n"+
			"    evaluators:\n      - evaluator: builtin.relevance\n")

	cfg, err := OpenEvalConfig(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	err = cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "target.name is required")
}
