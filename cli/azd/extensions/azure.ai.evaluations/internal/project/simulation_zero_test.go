// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Both counts use 0 as the "unstated" sentinel, so an explicitly written zero
// used to pass validation and be replaced with the default -- while the schema
// declares a minimum of 1. The editor refused the value and the CLI accepted
// it, and the reader was never told their number had been changed.
func TestAnExplicitZeroIsRefusedRatherThanDefaulted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "num_conversations: 0",
			body:    "model: gpt-4o\nnum_conversations: 0\n",
			wantErr: "simulation.num_conversations is 0",
		},
		{
			name:    "max_turns: 0",
			body:    "model: gpt-4o\nmax_turns: 0\n",
			wantErr: "simulation.max_turns is 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var sim Simulation
			err := yaml.Unmarshal([]byte(tt.body), &sim)

			require.Error(t, err, "an explicit zero was accepted and silently defaulted")
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Contains(t, err.Error(), "omit it for the default",
				"the message says what to write instead")
		})
	}
}

// Omission still means the documented default, which is the behavior the
// refusal above must not have cost.
func TestAnOmittedCountStillMeansTheDefault(t *testing.T) {
	t.Parallel()

	var sim Simulation
	require.NoError(t, yaml.Unmarshal([]byte("model: gpt-4o\n"), &sim))

	assert.Zero(t, sim.NumConversations, "unstated stays unstated on the struct")
	assert.Zero(t, sim.MaxTurns)
	assert.Equal(t, DefaultNumConversations, sim.Conversations())
	assert.NoError(t, sim.Validate())
}

// The decode must not start refusing values that are fine, which is the way a
// presence check most easily goes wrong.
func TestStatedCountsInRangeStillDecode(t *testing.T) {
	t.Parallel()

	var sim Simulation
	require.NoError(t, yaml.Unmarshal(
		[]byte("model: gpt-4o\nnum_conversations: 3\nmax_turns: 8\n"), &sim))

	assert.Equal(t, "gpt-4o", sim.Model)
	assert.Equal(t, 3, sim.NumConversations)
	assert.Equal(t, 8, sim.MaxTurns)
	assert.NoError(t, sim.Validate())

	// And an out-of-range stated value is still Validate's job, not the
	// decoder's -- the two checks answer different questions.
	var wide Simulation
	require.NoError(t, yaml.Unmarshal(
		[]byte("model: gpt-4o\nnum_conversations: 9\n"), &wide))
	assert.Error(t, wide.Validate())
}

// The bounds the decoder quotes have to be the bounds the schema declares, or
// the message sends a reader to a value their editor will refuse.
func TestTheZeroRefusalQuotesTheSchemaMinimums(t *testing.T) {
	t.Parallel()

	var sim Simulation
	err := yaml.Unmarshal([]byte("model: gpt-4o\nnum_conversations: 0\n"), &sim)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least 1")
	assert.Equal(t, 1, MinNumConversations)
	assert.Equal(t, 1, MinSimulationTurns)
}
