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

func TestAuthoredEvaluatorLevelsAreLocalMetadataOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		value  string
		levels []string
	}{
		{"known", "[turn, conversation]", []string{"turn", "conversation"}},
		{"future", "[future-level]", []string{"future-level"}},
		{"empty", "[]", nil},
		{"unknown shape", "{future: value}", nil},
		{"unknown entry", "[turn, {future: value}]", nil},
		{"nonstring entry", "[42]", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			body := "evaluators:\n  - name: rubric\n    $ref: ./does-not-exist.yaml\n" +
				"    future_setting: untouched\n    supported_evaluation_levels: " + tc.value + "\n"
			path := filepath.Join(dir, "azure.eval.yaml")
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			cfg, err := ReadAuthoredConfig(dir)
			require.NoError(t, err, "authoring must not resolve includes")
			entry, ok := cfg.Entry(SectionEvaluators, "rubric")
			require.True(t, ok)
			assert.Equal(t, tc.levels, entry.SupportedEvaluationLevels)
			assert.Equal(t, "./does-not-exist.yaml", entry.Ref)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, body, string(after))
		})
	}
}

func TestReadAuthoredDatasetResolvesOnlyItsLocalDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name        string
		declaration string
		want        string
	}{
		{"inline", "name: seeds\n    file: ./rows.jsonl", "rows.jsonl"},
		{"nested", "name: seeds\n    $ref: ./parts/dataset.yaml", filepath.Join("parts", "rows.jsonl")},
		{"overlay", "name: seeds\n    $ref: ./parts/dataset.yaml\n    file: ./override.jsonl", "override.jsonl"},
		{"ref only", "$ref: ./parts/dataset.yaml", filepath.Join("parts", "rows.jsonl")},
		{"registered only", "name: seeds\n    version: '1.0'", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "parts", "inner"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "dataset.yaml"),
				[]byte("$ref: ./inner/dataset.yaml\nfuture_metadata: keep\n"), 0o600))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "parts", "inner", "dataset.yaml"),
				[]byte("name: seeds\nfile: ../rows.jsonl\n"), 0o600))
			path := filepath.Join(dir, EvalConfigBase)
			body := "# Keep this\nfuture_setting: keep\ndatasets:\n  - " + tc.declaration + "\n" +
				"  - name: unrelated\n    $ref: ./missing.yaml\n" +
				"evaluators:\n  - $ref: ./also-missing.yaml\n"
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			decl, err := ReadAuthoredDataset(path, "seeds")
			require.NoError(t, err)
			require.NotNil(t, decl)
			assert.Equal(t, "seeds", decl.Name)
			want := tc.want
			if want != "" {
				want = filepath.Join(dir, want)
			}
			assert.Equal(t, want, decl.File)
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, body, string(after))
		})
	}
}

func TestReadAuthoredDatasetAbsentOrInvalid(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"absent", "", ""},
		{"unrelated", "datasets:\n  - name: unrelated\n    $ref: ./missing.yaml\n", ""},
		{"unrelated ref-only", "datasets:\n  - $ref: ./missing.yaml\n  - name: seeds\n", ""},
		{"missing include", "datasets:\n  - name: seeds\n    $ref: ./missing.yaml\n", "cannot read $ref"},
		{"wrong file type", "datasets:\n  - name: seeds\n    file: [rows.jsonl]\n", "file must be a string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, EvalConfigBase)
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))
			decl, err := ReadAuthoredDataset(path, "seeds")
			if tc.want != "" {
				require.ErrorContains(t, err, tc.want)
			} else {
				require.NoError(t, err)
			}
			if tc.name == "unrelated ref-only" {
				require.NotNil(t, decl)
				assert.Empty(t, decl.File)
			} else {
				assert.Nil(t, decl)
			}
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tc.body, string(after))
		})
	}
}
