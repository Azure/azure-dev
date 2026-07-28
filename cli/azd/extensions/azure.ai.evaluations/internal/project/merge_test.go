// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const handAuthored = `# Eval deployment spec
# Edited by hand - comments must survive generate.
evaluators:
  - name: safety-check          # hand-authored
    source: ./evaluators/safety-check.json

datasets:
  - name: support-golden
    source: ./datasets/old.jsonl
    version: "3"

evals:
  - name: pr-gate
    dataset: support-golden
    evaluators:
      - builtin.task_adherence
      - { name: safety-check, threshold: 4.0 }
`

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "azure.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// Regenerating must not destroy a hand-edited file.
func TestMergeArtifactRefs_PreservesCommentsAndSiblings(t *testing.T) {
	path := writeTemp(t, handAuthored)

	require.NoError(t, MergeArtifactRefs(path,
		[]ArtifactRef{{Name: "support-golden", Source: "./datasets/new.jsonl"}},
		[]ArtifactRef{{Name: "support-quality", Source: "./evaluators/support-quality.json"}},
	))

	out, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(out)

	require.Contains(t, text, "# Eval deployment spec", "leading comments must survive")
	require.Contains(t, text, "# hand-authored", "inline comments must survive")
	require.Contains(t, text, "./datasets/new.jsonl", "the matched source must be updated")
	require.NotContains(t, text, "./datasets/old.jsonl", "the old source must be replaced")
	require.Contains(t, text, "support-quality", "a new evaluator must be appended")
	require.Contains(t, text, "safety-check", "existing entries must be kept")

	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	ds, ok := cfg.Dataset("support-golden")
	require.True(t, ok)
	require.Equal(t, "./datasets/new.jsonl", ds.Source)
	require.Equal(t, "3", ds.Version, "sibling keys must not be disturbed")
	require.Len(t, cfg.Evaluators, 2)
}

// The eval's evaluator list must be left exactly as written.
func TestMergeArtifactRefs_DoesNotTouchEvals(t *testing.T) {
	path := writeTemp(t, handAuthored)
	require.NoError(t, MergeArtifactRefs(path, nil,
		[]ArtifactRef{{Name: "support-quality", Source: "./evaluators/q.json"}}))

	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err)
	g, ok := cfg.Group("pr-gate")
	require.True(t, ok)
	require.Len(t, g.Evaluators, 2)
	require.Equal(t, "builtin.task_adherence", g.Evaluators[0].Name)
	require.NotNil(t, g.Evaluators[1].Threshold)
}

// Sections absent from the file are created rather than erroring.
func TestMergeArtifactRefs_CreatesMissingSections(t *testing.T) {
	path := writeTemp(t, "evals:\n  - name: pr-gate\n    evaluators: [builtin.relevance]\n")

	require.NoError(t, MergeArtifactRefs(path,
		[]ArtifactRef{{Name: "d1", Source: "./datasets/d1.jsonl"}},
		[]ArtifactRef{{Name: "e1", Source: "./evaluators/e1.json"}},
	))

	cfg, err := LoadEvalConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)
	require.Len(t, cfg.Evaluators, 1)
	require.Equal(t, "d1", cfg.Datasets[0].Name)
}

// Running generate twice must be idempotent.
func TestMergeArtifactRefs_IsIdempotent(t *testing.T) {
	path := writeTemp(t, handAuthored)
	refs := []ArtifactRef{{Name: "support-golden", Source: "./datasets/new.jsonl"}}

	require.NoError(t, MergeArtifactRefs(path, refs, nil))
	first, err := os.ReadFile(path)
	require.NoError(t, err)

	require.NoError(t, MergeArtifactRefs(path, refs, nil))
	second, err := os.ReadFile(path)
	require.NoError(t, err)

	require.Equal(t, string(first), string(second),
		"merging the same references twice must not change the file")
}

func TestFingerprint_DetectsChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.jsonl")

	require.NoError(t, os.WriteFile(path, []byte(`{"query":"a"}`), 0o600))
	first, err := Fingerprint(path)
	require.NoError(t, err)

	again, err := Fingerprint(path)
	require.NoError(t, err)
	require.Equal(t, first, again, "unchanged content must hash the same")

	require.NoError(t, os.WriteFile(path, []byte(`{"query":"b"}`), 0o600))
	changed, err := Fingerprint(path)
	require.NoError(t, err)
	require.NotEqual(t, first, changed, "changed content must hash differently")
}

func TestFingerprintKey_IsEnvSafe(t *testing.T) {
	require.Equal(t, "EVAL_FINGERPRINT_DATASET_SUPPORT_GOLDEN",
		FingerprintKey("dataset", "support-golden"))
	require.Equal(t, "EVAL_FINGERPRINT_EVALUATOR_MY_EVAL_1",
		FingerprintKey("evaluator", "my.eval-1"))
}
