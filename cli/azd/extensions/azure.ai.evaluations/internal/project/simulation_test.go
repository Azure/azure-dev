// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bounds come from the REST contract. They are checked locally so an
// out-of-range value is refused before a run is created rather than after one
// is billed. ADO 5631478.
func TestSimulationValidate_Bounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sim     Simulation
		wantErr string
	}{
		{
			name: "the documented shape",
			sim:  Simulation{Model: "gpt-4o-mini", NumConversations: 1, MaxTurns: 5},
		},
		{
			name: "counts at the low bound",
			sim:  Simulation{Model: "gpt-4o-mini", NumConversations: 1, MaxTurns: 1},
		},
		{
			name: "counts at the high bound",
			sim:  Simulation{Model: "gpt-4o-mini", NumConversations: 5, MaxTurns: 20},
		},
		{
			name: "both counts unstated",
			sim:  Simulation{Model: "gpt-4o-mini"},
		},
		{
			name:    "no model to speak with",
			sim:     Simulation{NumConversations: 1},
			wantErr: "simulation.model is required",
		},
		{
			name:    "no conversations at all",
			sim:     Simulation{Model: "gpt-4o-mini", NumConversations: 0 - 1},
			wantErr: "num_conversations is -1",
		},
		{
			name:    "one conversation past the cap",
			sim:     Simulation{Model: "gpt-4o-mini", NumConversations: 6},
			wantErr: "num_conversations is 6",
		},
		{
			name:    "a conversation of no turns",
			sim:     Simulation{Model: "gpt-4o-mini", MaxTurns: 0 - 1},
			wantErr: "max_turns is -1",
		},
		{
			name:    "one turn past the cap",
			sim:     Simulation{Model: "gpt-4o-mini", MaxTurns: 21},
			wantErr: "max_turns is 21",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.sim.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// An eval with no simulation block is every eval that exists today, and
// validating one must not invent a requirement for it.
func TestSimulationValidate_NilIsNotASimulation(t *testing.T) {
	t.Parallel()

	var sim *Simulation
	assert.NoError(t, sim.Validate())
}

// The default lives in one place so two call sites cannot disagree about what
// an unstated count means.
func TestSimulationConversations_AppliesTheDefault(t *testing.T) {
	t.Parallel()

	var missing *Simulation
	assert.Equal(t, DefaultNumConversations, missing.Conversations())
	assert.Equal(t, DefaultNumConversations, (&Simulation{Model: "m"}).Conversations())
	assert.Equal(t, 3, (&Simulation{Model: "m", NumConversations: 3}).Conversations())
}

// The error has to name the value the reader has in front of them, and what
// the field will take instead, so the file can be corrected without guessing.
func TestSimulationValidate_ErrorsNameValueAndRange(t *testing.T) {
	t.Parallel()

	err := (&Simulation{Model: "gpt-4o-mini", NumConversations: 9}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "9")
	assert.Contains(t, err.Error(), "1 to 5")

	err = (&Simulation{Model: "gpt-4o-mini", MaxTurns: 99}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "99")
	assert.Contains(t, err.Error(), "1 to 20")
}
