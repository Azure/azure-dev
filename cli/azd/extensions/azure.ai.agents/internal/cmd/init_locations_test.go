// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSupportedModelLocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		modelLocations []string
		wantSubset     bool
		wantLen        int
	}{
		{
			name:           "AllSupported",
			modelLocations: []string{"eastus", "westus"},
			wantSubset:     true,
			wantLen:        2,
		},
		{
			name:           "SomeUnsupported",
			modelLocations: []string{"eastus", "unsupportedregion"},
			wantSubset:     true,
			wantLen:        1,
		},
		{
			name:           "NoneSupported",
			modelLocations: []string{"unsupportedregion1", "unsupportedregion2"},
			wantSubset:     true,
			wantLen:        0,
		},
		{
			name:           "EmptyInput",
			modelLocations: []string{},
			wantSubset:     true,
			wantLen:        0,
		},
		{
			name:           "NilInput",
			modelLocations: nil,
			wantSubset:     true,
			wantLen:        0,
		},
	}

	supported := supportedRegionsForInit()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := supportedModelLocations(tt.modelLocations)
			require.Len(t, result, tt.wantLen)

			// Every returned location must be in the supported regions list
			for _, loc := range result {
				require.True(t, locationAllowed(loc, supported),
					"returned location %q should be in supported regions", loc)
			}
		})
	}
}

func TestSupportedModelLocationsDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	input := []string{"eastus", "unsupportedregion", "westus"}
	original := make([]string, len(input))
	copy(original, input)

	_ = supportedModelLocations(input)

	require.Equal(t, original, input, "input slice should not be mutated")
}
