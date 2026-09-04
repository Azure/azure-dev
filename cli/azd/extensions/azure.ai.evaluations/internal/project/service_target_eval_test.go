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
		Name: "support-agent-evals",
		AdditionalProperties: propsFrom(t, map[string]any{
			"datasets": []any{
				map[string]any{"name": "golden", "file": "./datasets/golden.jsonl"},
			},
			"evals": []any{
				map[string]any{
					"name":    "support-agent-smoke",
					"dataset": "golden",
					"evaluators": []any{
						map[string]any{"evaluator": "builtin.task_adherence"},
					},
					"target": map[string]any{"type": "agent", "name": "my-agent"},
				},
			},
		}),
	}

	cfg, err := EvalConfigFromService(svc, "")
	require.NoError(t, err)
	require.Len(t, cfg.Datasets, 1)
	require.Equal(t, "golden", cfg.Datasets[0].Name)

	// One service covers every eval in the file it pulled in, so the eval is
	// selected by its own name rather than by the service key.
	eval, err := cfg.Eval("support-agent-smoke")
	require.NoError(t, err)
	require.Equal(t, "golden", eval.Dataset)
	require.Len(t, eval.Evaluators, 1)
	require.Equal(t, "builtin.task_adherence", eval.Evaluators[0].Evaluator)
	require.Equal(t, "my-agent", eval.Target.Name)
}

func TestEvalConfigFromServiceRejectsEmptyService(t *testing.T) {
	_, err := EvalConfigFromService(&azdext.ServiceConfig{Name: "evals"}, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no eval configuration")
}

// Evals are immutable, so a change to an eval's own declaration has to be
// detectable. Upstream artifact fingerprints do not cover it: retargeting an
// eval at a different agent leaves the dataset and evaluators untouched.
func TestFingerprintGroupTracksMeaningfulChanges(t *testing.T) {
	base := Eval{
		Name:            "quality",
		Dataset:         "golden",
		Evaluators:      evalcore.EvaluatorList{{Evaluator: "builtin.task_adherence"}},
		Target:          &Target{Type: "agent", Name: "agent-a"},
		EvaluationLevel: EvaluationLevelTurn,
	}

	original, err := FingerprintGroup(base)
	require.NoError(t, err)

	same, err := FingerprintGroup(base)
	require.NoError(t, err)
	require.Equal(t, original, same, "an unchanged eval must keep its fingerprint")

	cases := map[string]func(g *Eval){
		"target": func(g *Eval) { g.Target = &Target{Type: "agent", Name: "agent-b"} },
		"evaluators": func(g *Eval) {
			g.Evaluators = append(g.Evaluators, evalcore.EvaluatorRef{Evaluator: "builtin.similarity"})
		},
		"judge deployment": func(g *Eval) {
			g.Evaluators = evalcore.EvaluatorList{{
				Evaluator:                "builtin.task_adherence",
				InitializationParameters: map[string]any{"deployment_name": "gpt-4o-mini"},
			}}
		},
		"version pin": func(g *Eval) {
			g.Evaluators = evalcore.EvaluatorList{{
				Evaluator: "builtin.task_adherence", Version: "2",
			}}
		},
		"evaluation level": func(g *Eval) { g.EvaluationLevel = EvaluationLevelConversation },
		"dataset":          func(g *Eval) { g.Dataset = "other" },
		"source": func(g *Eval) {
			g.Dataset = ""
			g.Source = &SourceDecl{Type: SourceTypeTraces, AgentName: "agent-a"}
		},
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

// The fingerprint covers substance only. The id is server-assigned, and name
// and description are what UpdateEvalParametersBody reaches — an edit confined
// to those is pushed in place, so it must not fork the run history.
func TestFingerprintGroupIgnoresIdNameAndDescription(t *testing.T) {
	base := Eval{
		Name:       "quality",
		Dataset:    "golden",
		Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.task_adherence"}},
	}
	original, err := FingerprintGroup(base)
	require.NoError(t, err)

	noisy := base
	noisy.ID = "eval_abc123"
	noisy.Name = "quality-renamed"
	noisy.Description = "reworded"

	digest, err := FingerprintGroup(noisy)
	require.NoError(t, err)
	require.Equal(t, original, digest)
}

// Editing one eval must not recreate its siblings: the unit compared is the
// eval's own subtree, never the file.
func TestFingerprintGroupIsScopedToOneEval(t *testing.T) {
	gate := Eval{
		Name:       "support-agent-gate",
		Dataset:    "prod-golden",
		Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.task_adherence"}},
	}
	regression := Eval{
		Name:       "support-agent-regression-eval",
		Dataset:    "support-agent-regression",
		Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.task_adherence"}},
	}

	before, err := FingerprintGroup(regression)
	require.NoError(t, err)

	gate.Evaluators = append(gate.Evaluators, evalcore.EvaluatorRef{Evaluator: "builtin.similarity"})

	after, err := FingerprintGroup(regression)
	require.NoError(t, err)
	require.Equal(t, before, after, "editing a sibling must leave this eval alone")
}
