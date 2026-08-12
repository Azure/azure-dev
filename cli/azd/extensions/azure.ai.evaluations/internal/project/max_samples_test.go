// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolveMaxSamples reads anything not above zero as "no cap", so a negative
// max_samples used to send the WHOLE dataset to a run that is billed per row --
// the opposite of what a cap asks for, and with nothing said about it.
func TestNegativeMaxSamplesIsRefused(t *testing.T) {
	cfg := &EvalConfig{
		Datasets: []DatasetDecl{{Name: "golden", Source: "./datasets/golden.jsonl"}},
		Evals: []Eval{{
			Name:            "support-quality",
			Dataset:         "golden",
			EvaluationLevel: "turn",
			MaxSamples:      -1,
			Evaluators:      evalcore.EvaluatorList{{Evaluator: "builtin.relevance"}},
		}},
	}

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_samples")
	assert.Contains(t, err.Error(), "support-quality", "the eval that carries it")
	assert.Contains(t, err.Error(), "-1", "and the value that was rejected")
}

// Zero is how a config says "send every row", and has to keep working.
func TestUnsetMaxSamplesIsStillAllowed(t *testing.T) {
	cfg := &EvalConfig{
		Datasets: []DatasetDecl{{Name: "golden", Source: "./datasets/golden.jsonl"}},
		Evals: []Eval{{
			Name:            "support-quality",
			Dataset:         "golden",
			EvaluationLevel: "turn",
			Evaluators:      evalcore.EvaluatorList{{Evaluator: "builtin.relevance"}},
		}},
	}
	require.NoError(t, cfg.Validate())

	cfg.Evals[0].MaxSamples = 25
	assert.NoError(t, cfg.Validate(), "a positive cap is the ordinary case")
}
