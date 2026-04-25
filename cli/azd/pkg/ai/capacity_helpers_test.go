// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCapacityValidForSku(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sku      AiModelSku
		capacity int32
		want     bool
	}{
		{"zero capacity returns false", AiModelSku{MinCapacity: 1, MaxCapacity: 100}, 0, false},
		{"negative capacity returns false", AiModelSku{MinCapacity: 1, MaxCapacity: 100}, -5, false},
		{"below minimum returns false", AiModelSku{MinCapacity: 10, MaxCapacity: 100}, 5, false},
		{"above maximum returns false", AiModelSku{MinCapacity: 1, MaxCapacity: 100}, 150, false},
		{"step misaligned returns false", AiModelSku{MinCapacity: 5, MaxCapacity: 100, CapacityStep: 5}, 7, false},
		{"step aligned with minimum returns true", AiModelSku{MinCapacity: 5, MaxCapacity: 100, CapacityStep: 5}, 10, true},
		{"valid without step returns true", AiModelSku{MinCapacity: 1, MaxCapacity: 100}, 50, true},
		{
			name:     "step without minimum uses step as baseline - aligned",
			sku:      AiModelSku{MaxCapacity: 100, CapacityStep: 10},
			capacity: 20,
			want:     true,
		},
		{
			name:     "step without minimum uses step as baseline - misaligned",
			sku:      AiModelSku{MaxCapacity: 100, CapacityStep: 10},
			capacity: 15,
			want:     false,
		},
		{
			name:     "below step baseline when min is zero returns false",
			sku:      AiModelSku{MaxCapacity: 100, CapacityStep: 10},
			capacity: 5,
			want:     false,
		},
		{
			name:     "no bounds returns true for positive",
			sku:      AiModelSku{},
			capacity: 10,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, capacityValidForSku(tt.sku, tt.capacity))
		})
	}
}

func TestCapacityStepBaseline(t *testing.T) {
	t.Parallel()

	require.Equal(t, int32(5), capacityStepBaseline(AiModelSku{MinCapacity: 5, CapacityStep: 10}))
	require.Equal(t, int32(10), capacityStepBaseline(AiModelSku{MinCapacity: 0, CapacityStep: 10}))
}

func TestMinimumValidCapacity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sku  AiModelSku
		want int32
	}{
		{"uses min when set", AiModelSku{MinCapacity: 7, CapacityStep: 3}, 7},
		{"falls back to step when min is zero", AiModelSku{MinCapacity: 0, CapacityStep: 5}, 5},
		{"falls back to 1 when both zero", AiModelSku{}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, minimumValidCapacity(tt.sku))
		})
	}
}

func TestCapacityFitsWithinQuota(t *testing.T) {
	t.Parallel()

	sku := AiModelSku{MinCapacity: 1, MaxCapacity: 100, CapacityStep: 1}

	t.Run("valid capacity within quota fits", func(t *testing.T) {
		t.Parallel()
		require.True(t, capacityFitsWithinQuota(sku, 25, 50))
	})

	t.Run("valid capacity above quota does not fit", func(t *testing.T) {
		t.Parallel()
		require.False(t, capacityFitsWithinQuota(sku, 25, 10))
	})

	t.Run("invalid capacity does not fit even under quota", func(t *testing.T) {
		t.Parallel()
		require.False(t, capacityFitsWithinQuota(AiModelSku{MinCapacity: 10}, 5, 100))
	})
}

func TestFallbackCapacityWithinQuota(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sku       AiModelSku
		remaining float64
		wantCap   int32
		wantOk    bool
	}{
		{
			name:      "zero remaining quota fails",
			sku:       AiModelSku{MinCapacity: 1, MaxCapacity: 100, CapacityStep: 1},
			remaining: 0,
			wantOk:    false,
		},
		{
			name:      "negative remaining quota fails",
			sku:       AiModelSku{MinCapacity: 1, MaxCapacity: 100, CapacityStep: 1},
			remaining: -1,
			wantOk:    false,
		},
		{
			name:      "remaining above max is clamped to max",
			sku:       AiModelSku{MinCapacity: 1, MaxCapacity: 50, CapacityStep: 1},
			remaining: 200,
			wantCap:   50,
			wantOk:    true,
		},
		{
			name:      "remaining below minimum fails",
			sku:       AiModelSku{MinCapacity: 100, MaxCapacity: 500, CapacityStep: 10},
			remaining: 50,
			wantOk:    false,
		},
		{
			name:      "no step returns upper bound",
			sku:       AiModelSku{MinCapacity: 1, MaxCapacity: 0, CapacityStep: 0},
			remaining: 42,
			wantCap:   42,
			wantOk:    true,
		},
		{
			name:      "step aligned with minimum",
			sku:       AiModelSku{MinCapacity: 7, MaxCapacity: 1000, CapacityStep: 5},
			remaining: 20,
			wantCap:   17,
			wantOk:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cap, ok := fallbackCapacityWithinQuota(tt.sku, tt.remaining)
			require.Equal(t, tt.wantOk, ok)
			if tt.wantOk {
				require.Equal(t, tt.wantCap, cap)
			}
		})
	}
}
