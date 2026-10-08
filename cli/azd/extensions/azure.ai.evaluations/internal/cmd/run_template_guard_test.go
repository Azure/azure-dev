// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"azureaieval/internal/exterrors"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A conversation-simulation seed row describes a conversation to create. It
// carries no query, because nobody has asked anything yet.
const seedRows = `{"test_case_description":"A delayed order.","simulation_configuration":{"desired_num_turns":4}}` + "\n" +
	`{"id":2,"test_case_description":"A customer disputes a charge."}` + "\n"

// Invoking an agent target over seed rows sent an empty {{item.query}}: the
// agent was asked nothing, and the evaluator then scored the seeded text rather
// than the target's answer. The scores looked ordinary, which is what made it
// worth refusing rather than warning. ADO 5631335.
func TestBuildRunDataSource_RefusesAnAgentTargetOverRowsWithoutQuery(t *testing.T) {
	configPath := writeDataset(t, seedRows)
	ec := unregisteredRunContext(t)

	_, _, err := ec.buildRunDataSource(context.Background(), &project.Eval{
		Name:            "retail-multiturn",
		Dataset:         "d",
		EvaluationLevel: project.EvaluationLevelConversation,
		Target:          &project.Target{Type: project.TargetTypeAgent, Name: "hero-agent"},
	}, configPath, 0)

	require.Error(t, err)
	// Names the eval to edit and the column that is missing. The name lives in
	// the structured message rather than an outer wrapper, because azd
	// serializes the structured error and would drop a %w prefix.
	assert.Contains(t, err.Error(), `eval "retail-multiturn"`)
	assert.Contains(t, err.Error(), `"query"`)

	// The corrective action carries what the rows do have, so a reader does not
	// have to open the dataset to see why the binding failed.
	var local *azdext.LocalError
	require.ErrorAs(t, err, &local)
	assert.Contains(t, local.Suggestion, "test_case_description")
	assert.Contains(t, local.Suggestion, "simulation_configuration")
	assert.Equal(t, exterrors.CodeInvalidParameter, local.Code)
}

// A model target reads the same column and fails the same way.
func TestBuildRunDataSource_RefusesAModelTargetOverRowsWithoutQuery(t *testing.T) {
	configPath := writeDataset(t, seedRows)
	ec := unregisteredRunContext(t)

	_, _, err := ec.buildRunDataSource(context.Background(), &project.Eval{
		Name:    "retail-multiturn",
		Dataset: "d",
		Target:  &project.Target{Type: project.TargetTypeModel, Name: "gpt-4o-mini"},
	}, configPath, 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `"query"`)
}

// The same rows with nothing to invoke are scored as they stand, which is the
// static shape. Refusing these too would block a legitimate eval.
func TestBuildRunDataSource_ScoresRowsWithoutQueryWhenThereIsNoTarget(t *testing.T) {
	configPath := writeDataset(t, seedRows)
	ec := unregisteredRunContext(t)

	ds, _, err := ec.buildRunDataSource(context.Background(), &project.Eval{
		Name:            "retail-static",
		Dataset:         "d",
		EvaluationLevel: project.EvaluationLevelConversation,
	}, configPath, 0)

	require.NoError(t, err)
	require.NotNil(t, ds)
	assert.Empty(t, ds.TemplateItemFields(), "nothing is invoked, so nothing is bound")
}

// The turn path is what every existing eval runs, and it must be untouched.
func TestBuildRunDataSource_TurnRowsWithAnAgentTargetStillRun(t *testing.T) {
	configPath := writeDataset(t, oneRow)
	ec := unregisteredRunContext(t)

	ds, _, err := ec.buildRunDataSource(context.Background(), &project.Eval{
		Name:    "nightly",
		Dataset: "d",
		Target:  &project.Target{Type: project.TargetTypeAgent, Name: "hero-agent"},
	}, configPath, 0)

	require.NoError(t, err)
	require.NotNil(t, ds)
	assert.Equal(t, []string{"query"}, ds.TemplateItemFields())
}
