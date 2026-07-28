// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"path/filepath"
	"testing"

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
