// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A declaration that satisfies every combination rule, so each test can break
// exactly one thing and attribute the refusal to it.
func runnableSimulation() *project.Eval {
	return &project.Eval{
		Name:            "retail-multiturn",
		Dataset:         "d",
		EvaluationLevel: project.EvaluationLevelConversation,
		Target:          &project.Target{Type: project.TargetTypeAgent, Name: "hero-agent"},
		Simulation: &project.Simulation{
			Model:            "gpt-4o-mini",
			NumConversations: 1,
			MaxTurns:         5,
		},
	}
}

// Spec §4: the combinations a simulation eval must and must not have. Each is
// refused before a run is created, because a run is billed whether or not the
// declaration made sense.
func TestRefuseUnrunnableSimulation_CombinationRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*project.Eval)
		wantErr string
	}{
		{
			name:   "the documented shape is accepted",
			mutate: func(*project.Eval) {},
		},
		{
			name:    "turn level cannot produce conversations",
			mutate:  func(e *project.Eval) { e.EvaluationLevel = project.EvaluationLevelTurn },
			wantErr: "evaluation_level is \"turn\"",
		},
		{
			name:    "an unstated level is named as unset rather than blank",
			mutate:  func(e *project.Eval) { e.EvaluationLevel = "" },
			wantErr: "evaluation_level is \"unset\"",
		},
		{
			name:    "source and simulation are two different origins for rows",
			mutate:  func(e *project.Eval) { e.Source = &project.SourceDecl{Type: project.SourceTypeTraces} },
			wantErr: "both source: and simulation:",
		},
		{
			name:    "a conversation needs someone to talk to",
			mutate:  func(e *project.Eval) { e.Target = nil },
			wantErr: "no target is declared",
		},
		{
			name:    "a named but empty target is still no target",
			mutate:  func(e *project.Eval) { e.Target = &project.Target{Type: project.TargetTypeAgent} },
			wantErr: "no target is declared",
		},
		{
			name:    "a model is not an agent",
			mutate:  func(e *project.Eval) { e.Target.Type = project.TargetTypeModel },
			wantErr: "target.type is model",
		},
		{
			name:    "seeds have to come from somewhere",
			mutate:  func(e *project.Eval) { e.Dataset = "" },
			wantErr: "no dataset is declared",
		},
		{
			name:    "the simulated user needs a model",
			mutate:  func(e *project.Eval) { e.Simulation.Model = "" },
			wantErr: "simulation.model is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			group := runnableSimulation()
			tt.mutate(group)

			err := refuseUnrunnableSimulation(group)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Contains(t, err.Error(), `eval "retail-multiturn"`,
				"the reader has to know which declaration to edit")
		})
	}
}

// Spec §13, acceptance tests 7 and 8: the bounds accept their ends and reject
// one past them.
func TestRefuseUnrunnableSimulation_NumericBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		conversations int
		maxTurns      int
		wantErr       string
	}{
		{name: "one conversation, one turn", conversations: 1, maxTurns: 1},
		{name: "five conversations, twenty turns", conversations: 5, maxTurns: 20},
		{name: "no conversations", conversations: 0 - 1, wantErr: "num_conversations is -1"},
		{name: "six conversations", conversations: 6, wantErr: "num_conversations is 6"},
		{name: "zero turns is one below the floor", maxTurns: 0 - 1, wantErr: "max_turns is -1"},
		{name: "twenty-one turns", maxTurns: 21, wantErr: "max_turns is 21"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			group := runnableSimulation()
			group.Simulation.NumConversations = tt.conversations
			group.Simulation.MaxTurns = tt.maxTurns

			err := refuseUnrunnableSimulation(group)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Spec §5: every row is validated before any service mutation, and mixed seed
// and completed-conversation rows are rejected.
func TestRefuseUnusableSeedRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rows    []map[string]any
		wantErr string
	}{
		{
			name: "the documented seed row",
			rows: []map[string]any{
				{"id": 1.0, "category": "Order Status", "test_case_description": "A delayed order.", "desired_num_turns": 4.0},
			},
		},
		{
			name: "a seed row without the optional turn count",
			rows: []map[string]any{{"test_case_description": "A delayed order."}},
		},
		{
			name: "no query is valid for this mode",
			rows: []map[string]any{{"test_case_description": "A delayed order.", "category": "Orders"}},
		},
		{
			name: "a completed conversation is not a scenario",
			rows: []map[string]any{
				{"test_case_description": "A delayed order."},
				{"messages": []any{map[string]any{"role": "user", "content": "hi"}}},
			},
			wantErr: "row 2 carries \"messages\"",
		},
		{
			name:    "a row with nothing to simulate",
			rows:    []map[string]any{{"category": "Orders"}},
			wantErr: "row 1 has no \"test_case_description\"",
		},
		{
			name:    "an empty description describes nothing",
			rows:    []map[string]any{{"test_case_description": ""}},
			wantErr: "row 1 has an empty or non-text",
		},
		{
			name:    "a description that is not text",
			rows:    []map[string]any{{"test_case_description": 42.0}},
			wantErr: "row 1 has an empty or non-text",
		},
		{
			name: "the offending row is named, not just the first",
			rows: []map[string]any{
				{"test_case_description": "fine"},
				{"test_case_description": "also fine"},
				{"test_case_description": "fine too", "desired_num_turns": 0.0},
			},
			wantErr: "row 3",
		},
		{
			name:    "a fractional turn count is not a number of turns",
			rows:    []map[string]any{{"test_case_description": "A delayed order.", "desired_num_turns": 2.5}},
			wantErr: "not a positive whole number",
		},
		{
			name:    "a negative turn count",
			rows:    []map[string]any{{"test_case_description": "A delayed order.", "desired_num_turns": 0 - 1.0}},
			wantErr: "not a positive whole number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := refuseUnusableSeedRows(runnableSimulation(), tt.rows)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// Spec §3 and §13 acceptance test 9: a per-row count is honored but stays
// bounded by the eval's own ceiling. Saying so beats a conversation that
// silently ends early.
func TestRefuseUnusableSeedRows_PerRowTurnsRespectTheCeiling(t *testing.T) {
	t.Parallel()

	group := runnableSimulation()
	group.Simulation.MaxTurns = 5

	withinBound := []map[string]any{{"test_case_description": "A delayed order.", "desired_num_turns": 5.0}}
	require.NoError(t, refuseUnusableSeedRows(group, withinBound),
		"a row asking for exactly the ceiling is satisfiable")

	pastBound := []map[string]any{{"test_case_description": "A delayed order.", "desired_num_turns": 6.0}}
	err := refuseUnusableSeedRows(group, pastBound)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "asks for 6 turns")
	assert.Contains(t, err.Error(), "max_turns is 5")

	// With no ceiling declared the service decides, so a per-row count is not
	// measured against a bound this eval never set.
	group.Simulation.MaxTurns = 0
	assert.NoError(t, refuseUnusableSeedRows(group, pastBound))
}

// An eval with no simulation block must not be sent down this path at all.
func TestBuildRunDataSource_NoSimulationBlockKeepsTheTurnPath(t *testing.T) {
	configPath := writeDataset(t, oneRow)
	ec := &evalContext{}

	ds, err := ec.buildRunDataSource(context.Background(), &project.Eval{
		Name:    "nightly",
		Dataset: "d",
		Target:  &project.Target{Type: project.TargetTypeAgent, Name: "hero-agent"},
	}, configPath, 0)

	require.NoError(t, err)
	require.NotNil(t, ds)
	assert.Equal(t, []string{"query"}, ds.TemplateItemFields(),
		"the turn path still binds the question it always did")
}
