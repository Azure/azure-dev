// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// The combination and bounds rules themselves live with every other rule an
// eval has to satisfy, in project.ValidateRunnable, so that `azd up` refuses
// what `eval run` refuses. They are exercised in project/runnable_test.go.
//
// What is checked here is the part only the run door can promise: that the
// refusal names the declaration to edit. ValidateRunnable carries no prefix on
// purpose -- the caller says whether it has an index to name -- so losing the
// name is a silent possibility rather than a compile error.
func TestSimulationRefusalNamesTheEval(t *testing.T) {
	t.Parallel()

	group := runnableSimulation()
	group.Dataset = ""
	group.Source = &project.SourceDecl{Type: project.SourceTypeTraces, AgentName: "a"}

	err := runnableEval(group)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `eval "retail-multiturn"`,
		"the reader has to know which declaration to edit")
	assert.Contains(t, err.Error(), "describe different runs")
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
				{
					"id":                    1.0,
					"category":              "Order Status",
					"test_case_description": "A delayed order.",
					"desired_num_turns":     4.0,
				},
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
			name:    "whitespace describes nothing",
			rows:    []map[string]any{{"test_case_description": " \t\r\n "}},
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

// The README prints seed rows for a reader to copy. Rows that the CLI would
// then refuse are worse than no example, so the documented shape is checked
// against the same guard a real run goes through.
func TestTheREADMESeedRowsAreAcceptedAsSeeds(t *testing.T) {
	t.Parallel()

	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	require.NoError(t, err)

	block := fencedBlockAfter(t, string(readme), "### Simulating multi-turn conversations", "jsonl")

	var rows []map[string]any
	for line := range strings.Lines(block) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var row map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &row), "documented seed row is not JSON: %s", line)
		rows = append(rows, row)
	}
	require.NotEmpty(t, rows, "the README no longer shows seed rows")

	group := runnableSimulation()
	group.Simulation.MaxTurns = project.MaxSimulationTurns
	require.NoError(t, refuseUnusableSeedRows(group, rows),
		"the README documents seed rows the CLI would refuse")
}

// fencedBlockAfter returns the first fenced block of the given language that
// follows a heading.
func fencedBlockAfter(t *testing.T, readme, heading, language string) string {
	t.Helper()

	readme = strings.ReplaceAll(readme, "\r\n", "\n")

	at := strings.Index(readme, heading)
	require.NotEqual(t, -1, at,
		"the README no longer has the %q section; retarget this test rather than deleting it",
		heading)

	fence := "```" + language + "\n"
	rest := readme[at+len(heading):]
	start := strings.Index(rest, fence)
	require.NotEqual(t, -1, start, "no %s block follows %q", language, heading)

	rest = rest[start+len(fence):]
	end := strings.Index(rest, "```")
	require.NotEqual(t, -1, end, "the block's fence is unterminated")

	return rest[:end]
}

// An eval with no simulation block must not be sent down this path at all.
func TestBuildRunDataSource_NoSimulationBlockKeepsTheTurnPath(t *testing.T) {
	configPath := writeDataset(t, oneRow)
	ec := unregisteredRunContext(t)

	ds, _, err := ec.buildRunDataSource(context.Background(), &project.Eval{
		Name:    "nightly",
		Dataset: "d",
		Target:  &project.Target{Type: project.TargetTypeAgent, Name: "hero-agent"},
	}, configPath, 0)

	require.NoError(t, err)
	require.NotNil(t, ds)
	assert.Equal(t, []string{"query"}, ds.TemplateItemFields(),
		"the turn path still binds the question it always did")
}
