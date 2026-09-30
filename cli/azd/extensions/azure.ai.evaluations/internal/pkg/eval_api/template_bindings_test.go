// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTemplateItemFields_ReadsWhatTheTemplateBinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ds   *EvalRunDataSource
		want []string
	}{
		{
			name: "agent target binds query",
			ds:   NewAgentTargetDataSource("hero-agent", nil),
			want: []string{"query"},
		},
		{
			name: "model target binds query",
			ds:   NewModelTargetDataSource("gpt-4o-mini"),
			want: []string{"query"},
		},
		{
			name: "dataset only binds nothing",
			ds:   NewDatasetOnlyDataSource(),
			want: nil,
		},
		{
			name: "nil data source binds nothing",
			ds:   nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.ds.TemplateItemFields())
		})
	}
}

// Whitespace inside the braces is still a binding, and a template reading two
// columns needs both reported -- sorted, so the same dataset reads the same way
// twice.
func TestTemplateItemFields_HandlesSpacingAndMultipleBindings(t *testing.T) {
	t.Parallel()

	ds := &EvalRunDataSource{
		InputMessages: &EvalRunInputMessages{
			Type: "template",
			Template: []EvalRunMessageTemplate{
				{Role: "system", Content: "context: {{ item.context }}"},
				{Role: "user", Content: "{{item.query}} and {{item.query}}"},
			},
		},
	}

	assert.Equal(t, []string{"context", "query"}, ds.TemplateItemFields())
}

// A conversation seed row carries test_case_description and no query at all.
// Invoking a target against it sends an empty question, and the score that
// comes back describes the seeded text rather than the target. ADO 5631335.
func TestMissingTemplateFields_CatchesRowsWithoutTheBoundColumn(t *testing.T) {
	t.Parallel()

	seeds := []map[string]any{
		{
			"test_case_description":    "A customer asks about a delayed order.",
			"simulation_configuration": map[string]any{"desired_num_turns": 4},
		},
		{"id": 2, "test_case_description": "A customer disputes a charge."},
	}

	missing := NewAgentTargetDataSource("hero-agent", nil).MissingTemplateFields(seeds)

	assert.Equal(t, []string{"query"}, missing)
}

func TestMissingTemplateFields_TurnRowsBindCleanly(t *testing.T) {
	t.Parallel()

	rows := []map[string]any{
		{"query": "where is my order?", "ground_truth": "order status"},
	}

	assert.Empty(t, NewAgentTargetDataSource("hero-agent", nil).MissingTemplateFields(rows))
}

// A column only some rows carry is a sparse dataset, which is the author's
// business. Only a column no row has is a request that cannot be answered.
func TestMissingTemplateFields_SparseColumnIsNotMissing(t *testing.T) {
	t.Parallel()

	rows := []map[string]any{
		{"query": "where is my order?"},
		{"ground_truth": "no query on this row"},
	}

	assert.Empty(t, NewAgentTargetDataSource("hero-agent", nil).MissingTemplateFields(rows))
}

// A data source that invokes nothing reads no column, so no dataset can be
// missing one.
func TestMissingTemplateFields_DatasetOnlyNeverMisses(t *testing.T) {
	t.Parallel()

	seeds := []map[string]any{{"test_case_description": "anything"}}

	assert.Empty(t, NewDatasetOnlyDataSource().MissingTemplateFields(seeds))
}
