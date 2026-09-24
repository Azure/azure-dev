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

func TestAuthoredConfigRejectsAmbiguousDocumentsWithoutMutation(t *testing.T) {
	for _, body := range []string{
		"evals: []\n---\nevals: [{name: owned}]\n",
		"evals: []\n...\n---\nevals: [{name: owned}]\n",
		"evals: []\n---\n",
		"evals: []\n---\n[broken",
		"datasets: []\ndatasets: [{name: owned}]\n",
		"evals: []\nevals: [{name: owned}]\n",
		"&key evals: []\n*key : [{name: owned}]\n",
		"x-key: &key evals\n*key : [{name: owned}]\n",
		"? [complex, key]\n: value\nevals: []\n",
		"x-defaults: &defaults {evals: [{name: owned}]}\n<<: *defaults\n",
		"<<: {datasets: [{name: golden}], evals: [{name: owned}]}\n",
		"<<: [{evals: [{name: owned}]}, {datasets: [{name: golden}]}]\n",
	} {
		t.Run("", func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, EvalConfigBase)
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			_, err := ReadAuthoredConfig(dir)
			require.Error(t, err)
			require.Error(t, ApplyScaffold(dir, ScaffoldWrite{Evals: []Eval{{Name: "new"}}}))
			_, _, err = UpsertCatalogEntry(dir, SectionDatasets, "new", "file", "./new.jsonl")
			require.Error(t, err)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, body, string(after))
		})
	}
}

func TestAuthoredConfigSingleDocumentSyntaxRemainsValid(t *testing.T) {
	for _, body := range []string{
		"", "# just a comment\n", "---\nevals: []\n...\n# end\n",
		"x-notes: |\n  ---\n  ...\nevals: []\n",
		"x-template: &template {custom: true}\nx-copy: *template\nevals: []\n",
		"datasets:\n  - name: unrelated\n    $ref: ./not-present.yaml\nevals: []\n",
		"\"<<\": {metadata: retained}\nevals: []\n",
	} {
		t.Run("", func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, EvalConfigBase)
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			_, err := ReadAuthoredConfig(dir)
			require.NoError(t, err)
			require.NoError(t, ApplyScaffold(dir, ScaffoldWrite{Evals: []Eval{{Name: "new"}}}))
			authored, err := ReadAuthoredConfig(dir)
			require.NoError(t, err)
			assert.Contains(t, authored.Names(SectionEvals), "new")
		})
	}
}
