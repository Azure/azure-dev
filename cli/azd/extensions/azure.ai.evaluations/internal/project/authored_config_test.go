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
