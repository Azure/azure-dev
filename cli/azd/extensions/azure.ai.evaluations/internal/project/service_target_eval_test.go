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
		Name: "support-agent-smoke",
		AdditionalProperties: propsFrom(t, map[string]any{
			"dataset":    map[string]any{"name": "golden", "source": "./datasets/golden.jsonl"},
			"evaluators": []any{"builtin.task_adherence"},
			"target":     map[string]any{"type": "agent", "name": "my-agent"},
		}),
	}

	cfg, err := EvalConfigFromService(svc, "")
	require.NoError(t, err)
	require.NotNil(t, cfg.Dataset)
	require.Equal(t, "golden", cfg.Dataset.Name)
	require.Len(t, cfg.Evaluators, 1)
	require.Equal(t, "builtin.task_adherence", cfg.Evaluators[0].Name)
	require.Equal(t, "my-agent", cfg.Target.Name)

	// The eval's name is the service key, which is what makes one service per
	// eval work without the body repeating it.
	require.Equal(t, "support-agent-smoke", cfg.Eval(svc.Name).Name)
}

func TestEvalConfigFromServiceRejectsEmptyService(t *testing.T) {
	_, err := EvalConfigFromService(&azdext.ServiceConfig{Name: "evals"}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no eval configuration")
}

// Evals are immutable, so a change to the eval's own declaration has to be
// detectable. Upstream artifact fingerprints do not cover it: retargeting an
// eval at a different agent leaves the dataset and evaluators untouched.
func TestFingerprintGroupTracksMeaningfulChanges(t *testing.T) {
	base := Eval{
		Name:       "quality",
		Dataset:    "golden",
		Evaluators: evalcore.EvaluatorList{{Name: "builtin.task_adherence"}},
		Target:     &Target{Type: "agent", Name: "agent-a"},
		Options:    &Options{EvaluationLevel: EvaluationLevelTurn},
	}

	original, err := FingerprintGroup(base)
	require.NoError(t, err)

	same, err := FingerprintGroup(base)
	require.NoError(t, err)
	require.Equal(t, original, same, "an unchanged eval must keep its fingerprint")

	cases := map[string]func(g *Eval){
		"target": func(g *Eval) { g.Target = &Target{Type: "agent", Name: "agent-b"} },
		"evaluators": func(g *Eval) {
			g.Evaluators = append(g.Evaluators, evalcore.EvaluatorRef{Name: "builtin.similarity"})
		},
		"judge deployment": func(g *Eval) {
			g.Evaluators = evalcore.EvaluatorList{{
				Name:                     "builtin.task_adherence",
				InitializationParameters: map[string]any{"deployment_name": "gpt-4o-mini"},
			}}
		},
		"options": func(g *Eval) { g.Options = &Options{EvaluationLevel: EvaluationLevelConversation} },
		"dataset": func(g *Eval) { g.Dataset = "other" },
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
	base := Eval{
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
