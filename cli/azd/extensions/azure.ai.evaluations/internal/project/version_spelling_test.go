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

// An unquoted version is a number, and `azd up` reads the configuration through
// the service entry rather than off disk. That route arrives as protobuf, whose
// only numeric kind is a double, so `1` and `1.0` are the same value by the
// time this extension is handed it -- and it renders as "1", while reading the
// same file off disk gives "1.0".
//
// The spelling is destroyed before this code runs, so there is nothing here to
// recover it from: emitting "1.0" would break a config that meant 1, and
// rejecting the number would fail a file that `azd ai eval run` accepts. This
// pins the divergence so it is visible and cannot widen unnoticed.
//
// The fix available to a user is to quote it, which both routes preserve.
func TestNumericVersionLosesItsSpellingOnTheServiceRoute(t *testing.T) {
	fromService := func(t *testing.T, version any) string {
		t.Helper()
		props, err := structpb.NewStruct(map[string]any{
			"evals": []any{map[string]any{
				"name":             "support-quality",
				"dataset":          "golden",
				"evaluation_level": "turn",
				"evaluators": []any{map[string]any{
					"evaluator": "builtin.relevance",
					"version":   version,
				}},
			}},
		})
		require.NoError(t, err)

		cfg, err := EvalConfigFromService(
			&azdext.ServiceConfig{Name: "evals", AdditionalProperties: props}, "")
		require.NoError(t, err)
		require.Len(t, cfg.Evals, 1)
		require.Len(t, cfg.Evals[0].Evaluators, 1)
		return cfg.Evals[0].Evaluators[0].Version
	}

	assert.Equal(t, "1", fromService(t, 1.0),
		"1.0 and 1 are one number in protobuf, so the decimal cannot survive")
	assert.Equal(t, "1.5", fromService(t, 1.5),
		"a fractional part is not lost, only a trailing zero")
	assert.Equal(t, "1.0", fromService(t, "1.0"),
		"quoting is what carries the spelling through, and is the advice to give")
}

// The same file read off disk keeps what the user wrote, which is the half of
// the divergence that behaves.
func TestQuotedAndUnquotedVersionsOffDisk(t *testing.T) {
	cfg, err := DecodeEvalConfig([]byte(`
evals:
  - name: support-quality
    dataset: golden
    evaluation_level: turn
    evaluators:
      - evaluator: builtin.relevance
        version: 1.0
`), "eval.yaml")
	require.NoError(t, err)
	assert.Equal(t, "1.0", cfg.Evals[0].Evaluators[0].Version,
		"YAML hands a string field the scalar as written")
}
