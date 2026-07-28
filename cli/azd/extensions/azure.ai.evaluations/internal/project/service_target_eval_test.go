// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func propsFrom(t *testing.T, values map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(values)
	require.NoError(t, err)
	return s
}

// A service authored as `host:` + `$ref: ./evals/azure.yaml` has its relative
// source paths written against the included file, not the project root.
// ResolveFileRefs inlines the content without rebasing them, so the base has to
// come from the $ref value.
func TestServiceRelativeDirUsesRefDirectory(t *testing.T) {
	svc := &azdext.ServiceConfig{
		Name: "evals",
		AdditionalProperties: propsFrom(t, map[string]any{
			"$ref": "./evals/azure.yaml",
		}),
	}
	require.Equal(t, filepath.FromSlash("evals"), serviceRelativeDir(svc))
}

// A nested include keeps its own directory.
func TestServiceRelativeDirUsesNestedRefDirectory(t *testing.T) {
	svc := &azdext.ServiceConfig{
		Name: "evals",
		AdditionalProperties: propsFrom(t, map[string]any{
			"$ref": "./config/evals/azure.yaml",
		}),
	}
	require.Equal(t, filepath.FromSlash("config/evals"), serviceRelativeDir(svc))
}

// Without a $ref the service's own relative path is the base.
func TestServiceRelativeDirFallsBackToRelativePath(t *testing.T) {
	svc := &azdext.ServiceConfig{
		Name:         "evals",
		RelativePath: "evals",
		AdditionalProperties: propsFrom(t, map[string]any{
			"datasets": []any{},
		}),
	}
	require.Equal(t, "evals", serviceRelativeDir(svc))
}

// With neither, sources resolve against the project root.
func TestServiceRelativeDirDefaultsToProjectRoot(t *testing.T) {
	require.Equal(t, ".", serviceRelativeDir(&azdext.ServiceConfig{Name: "evals"}))
	require.Equal(t, ".", serviceRelativeDir(nil))
}

// An inline config still parses when no project root is available to resolve
// includes against.
func TestEvalConfigFromServiceReadsInlineConfig(t *testing.T) {
	svc := &azdext.ServiceConfig{
		Name: "evals",
		AdditionalProperties: propsFrom(t, map[string]any{
			"datasets": []any{
				map[string]any{"name": "golden", "source": "./datasets/golden.jsonl"},
			},
			"evalGroups": []any{
				map[string]any{
					"name":       "quality",
					"dataset":    "golden",
					"evaluators": []any{"builtin.task_adherence"},
					"target":     map[string]any{"type": "agent", "name": "my-agent"},
				},
			},
		}),
	}

	cfg, err := EvalConfigFromService(svc, "")
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)
	require.Equal(t, "golden", cfg.Datasets[0].Name)
	require.Len(t, cfg.EvalGroups, 1)
	require.Len(t, cfg.EvalGroups[0].Evaluators, 1)
	require.Equal(t, "builtin.task_adherence", cfg.EvalGroups[0].Evaluators[0].Name)
}

func TestEvalConfigFromServiceRejectsEmptyService(t *testing.T) {
	_, err := EvalConfigFromService(&azdext.ServiceConfig{Name: "evals"}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no eval configuration")
}

// Groups are immutable, so a change to the group's own declaration has to be
// detectable. Upstream artifact fingerprints do not cover it: retargeting a
// group at a different agent leaves the dataset and evaluators untouched.
func TestFingerprintGroupTracksMeaningfulChanges(t *testing.T) {
	base := EvalGroup{
		Name:       "quality",
		Dataset:    "golden",
		Evaluators: evalcore.EvaluatorList{{Name: "builtin.task_adherence"}},
		Target:     &Target{Type: "agent", Name: "agent-a"},
		Options:    &Options{EvalModel: "gpt-4.1-nano"},
	}

	original, err := FingerprintGroup(base)
	require.NoError(t, err)

	same, err := FingerprintGroup(base)
	require.NoError(t, err)
	require.Equal(t, original, same, "an unchanged group must keep its fingerprint")

	cases := map[string]func(g *EvalGroup){
		"target": func(g *EvalGroup) { g.Target = &Target{Type: "agent", Name: "agent-b"} },
		"evaluators": func(g *EvalGroup) {
			g.Evaluators = append(g.Evaluators, evalcore.EvaluatorRef{Name: "builtin.similarity"})
		},
		"options": func(g *EvalGroup) { g.Options = &Options{EvalModel: "gpt-4o-mini"} },
		"dataset": func(g *EvalGroup) { g.Dataset = "other" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := base
			changed.Evaluators = append(evalcore.EvaluatorList(nil), base.Evaluators...)
			mutate(&changed)

			digest, err := FingerprintGroup(changed)
			require.NoError(t, err)
			require.NotEqual(t, original, digest, "changing %s must change the fingerprint", name)
		})
	}
}

// Server-assigned and cosmetic fields must not force a recreate.
func TestFingerprintGroupIgnoresIdAndDescription(t *testing.T) {
	base := EvalGroup{
		Name:       "quality",
		Dataset:    "golden",
		Evaluators: evalcore.EvaluatorList{{Name: "builtin.task_adherence"}},
	}
	original, err := FingerprintGroup(base)
	require.NoError(t, err)

	noisy := base
	noisy.ID = "eval_abc123"
	noisy.Description = "reworded"

	digest, err := FingerprintGroup(noisy)
	require.NoError(t, err)
	require.Equal(t, original, digest)
}
