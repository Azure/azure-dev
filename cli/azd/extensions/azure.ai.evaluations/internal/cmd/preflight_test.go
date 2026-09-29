// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func relevanceSchema(required ...string) map[string]*eval_api.EvaluatorSummary {
	return map[string]*eval_api.EvaluatorSummary{
		"builtin.relevance": {
			Name:                      "builtin.relevance",
			SupportedEvaluationLevels: []string{"turn"},
			Definition: &eval_api.EvaluatorContract{
				InitParameters: &eval_api.JSONSchema{Required: required},
			},
		},
	}
}

func evalRequiring(params map[string]any) *project.Eval {
	return &project.Eval{
		Name:            "quality",
		Dataset:         "golden",
		EvaluationLevel: "turn",
		Evaluators: evalcore.EvaluatorList{{
			Evaluator:                "builtin.relevance",
			InitializationParameters: params,
		}},
	}
}

// The same requirement was checked while building the request, which runs after
// the datasets and evaluators have been pushed. A missing judge deployment
// therefore cost an immutable dataset version per attempt, and the version
// number climbs whether or not the eval is ever created.
func TestCreateRefusesAMissingJudgeBeforePublishing(t *testing.T) {
	err := checkEvaluatorRequirements(evalRequiring(nil), relevanceSchema("deployment_name"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployment_name")
	assert.Contains(t, err.Error(), "builtin.relevance")
}

// Either spelling of the judge deployment satisfies it, the same way the
// request builder binds whichever the evaluator publishes.
func TestEitherJudgeSpellingSatisfiesTheRequirement(t *testing.T) {
	for _, declared := range []string{"deployment_name", "model"} {
		err := checkEvaluatorRequirements(
			evalRequiring(map[string]any{declared: "o4-mini"}),
			relevanceSchema("deployment_name"))

		assert.NoErrorf(t, err, "%q is the same parameter under the other name", declared)
	}
}

// evaluation_level is supplied from the eval's own declaration rather than
// written under initialization_parameters, so requiring it must not refuse a
// configuration that sets the level.
func TestRequiredEvaluationLevelComesFromTheDeclaration(t *testing.T) {
	err := checkEvaluatorRequirements(
		evalRequiring(map[string]any{"deployment_name": "o4-mini"}),
		relevanceSchema("deployment_name", "evaluation_level"))

	assert.NoError(t, err)
}

// An evaluator the listing did not describe leaves the service with the last
// word, which is what happened before this check existed.
func TestAnUnknownEvaluatorIsLeftToTheService(t *testing.T) {
	err := checkEvaluatorRequirements(evalRequiring(nil),
		map[string]*eval_api.EvaluatorSummary{})

	assert.NoError(t, err, "nothing published to check against")
}

// A level the evaluator does not support is the other thing settled by the
// contract alone, so it is worth catching before a publish too.
func TestAnUnsupportedLevelIsRefusedBeforePublishing(t *testing.T) {
	schemas := relevanceSchema("deployment_name")
	schemas["builtin.relevance"].SupportedEvaluationLevels = []string{"conversation"}

	err := checkEvaluatorRequirements(
		evalRequiring(map[string]any{"deployment_name": "o4-mini"}), schemas)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "turn")
}
