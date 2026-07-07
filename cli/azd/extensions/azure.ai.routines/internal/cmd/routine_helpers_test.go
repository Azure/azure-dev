// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ─── boolStr ─────────────────────────────────────────────────────────────────

func TestBoolStr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		val  *bool
		want string
	}{
		{"nil", nil, "unknown"},
		{"true", new(true), "true"},
		{"false", new(false), "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, boolStr(tt.val))
		})
	}
}

// ─── sortedKeys ──────────────────────────────────────────────────────────────

func TestSortedKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		m    map[string]int
		want []string
	}{
		{"nil map", nil, nil},
		{"empty map", map[string]int{}, nil},
		{"single", map[string]int{"a": 1}, []string{"a"}},
		{"multiple", map[string]int{"c": 3, "a": 1, "b": 2}, []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sortedKeys(tt.m)
			assert.Equal(t, tt.want, got)
		})
	}
}
