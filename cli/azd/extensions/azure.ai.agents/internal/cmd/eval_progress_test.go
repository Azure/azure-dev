// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---- durationText ----

func TestDurationText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"less than a second", 500 * time.Millisecond, "less than a second"},
		{"exactly 1 second", 1 * time.Second, "1 second"},
		{"multiple seconds", 30 * time.Second, "30 seconds"},
		{"exactly 59 seconds", 59 * time.Second, "59 seconds"},
		{"exactly 1 minute", 60 * time.Second, "1 minute"},
		{"exactly 2 minutes", 120 * time.Second, "2 minutes"},
		{"1 minute 30 seconds", 90 * time.Second, "1m 30s"},
		{"2 minutes 15 seconds", 135 * time.Second, "2m 15s"},
		{"zero", 0, "less than a second"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, durationText(tt.duration))
		})
	}
}
