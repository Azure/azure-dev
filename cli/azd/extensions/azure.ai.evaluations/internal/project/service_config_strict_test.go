// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func serviceWith(t *testing.T, props map[string]any) *azdext.ServiceConfig {
	t.Helper()
	s, err := structpb.NewStruct(props)
	require.NoError(t, err)
	return &azdext.ServiceConfig{Name: "support-agent-evals", AdditionalProperties: s}
}

// `azd up` reads the configuration through the service entry, not off disk, and
// that route used json.Unmarshal -- which drops unknown keys silently. So a
// misspelled key was named by `azd ai eval run` and ignored by `azd up`, and
// the setting the author thought they had wrote simply did not exist.
//
// Both routes now go through the same strict decoder.
func TestEvalConfigFromServiceRejectsAMistypedKey(t *testing.T) {
	svc := serviceWith(t, map[string]any{
		"evals": []any{map[string]any{
			"name":             "support-agent-eval",
			"evaulators":       []any{}, // the typo `azd ai eval run` already catches
			"evaluation_level": "turn",
		}},
	})

	_, err := EvalConfigFromService(svc, "")

	require.Error(t, err, "a key this extension does not know is a typo, on either route")
	assert.Contains(t, err.Error(), "evaulators")
	assert.Contains(t, err.Error(), "evaluators", "the near miss is what makes it actionable")
}

// The keys the schema does know still decode, so the strictness did not close
// the door on the authoring style it is meant to serve.
func TestEvalConfigFromServiceAcceptsADeclaredConfig(t *testing.T) {
	svc := serviceWith(t, map[string]any{
		"datasets": []any{map[string]any{"name": "golden", "file": "./datasets/golden.jsonl"}},
		"evals": []any{map[string]any{
			"name":             "support-agent-eval",
			"dataset":          "golden",
			"evaluation_level": "turn",
		}},
	})

	cfg, err := EvalConfigFromService(svc, "")

	require.NoError(t, err)
	require.Len(t, cfg.Evals, 1)
	assert.Equal(t, "support-agent-eval", cfg.Evals[0].Name)
	require.Len(t, cfg.Datasets, 1)
	assert.Equal(t, "golden", cfg.Datasets[0].Name)
}

// An include reached without a project directory is refused rather than
// discarded.
//
// Resolution was skipped when there was nowhere to resolve against, and the
// directive was then deleted so the strict decoder would not trip on it. This
// fixture shows the cost: a service mixing an include with inline content
// deployed only the inline half, and the failure surfaced as a missing eval
// rather than as the include nobody could read.
func TestARefWithoutAProjectRootIsRefused(t *testing.T) {
	svc := serviceWith(t, map[string]any{
		"$ref": "./evals/azure.eval.yaml",
		"evals": []any{map[string]any{
			"name":             "support-agent-eval",
			"evaluation_level": "turn",
		}},
	})

	_, err := EvalConfigFromService(svc, "")

	require.Error(t, err, "half a configuration is not a configuration")
	assert.Contains(t, err.Error(), "$ref")
}
