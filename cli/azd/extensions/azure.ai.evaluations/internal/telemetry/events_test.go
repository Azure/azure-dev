// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"maps"
	"testing"
)

func TestInitCompletedWireContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    InitSource
		wantValue string
	}{
		{name: "traces", source: InitSourceTraces, wantValue: "traces"},
		{name: "dataset", source: InitSourceDataset, wantValue: "dataset"},
		{name: "unknown", source: InitSourceUnknown, wantValue: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := InitCompleted(tt.source)
			if event.Name != "init.completed" {
				t.Fatalf("event name = %q, want %q", event.Name, "init.completed")
			}

			wantAttributes := map[string]string{"source": tt.wantValue}
			if !maps.Equal(event.Attributes, wantAttributes) {
				t.Fatalf("event attributes = %#v, want %#v", event.Attributes, wantAttributes)
			}
		})
	}
}

// A source this package does not know must not reach the wire as itself, or
// the attribute stops being a closed set the moment init grows a third source.
func TestNewInitSourceClosesOverUnknownValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		given string
		want  InitSource
	}{
		{name: "traces", given: "traces", want: InitSourceTraces},
		{name: "dataset", given: "dataset", want: InitSourceDataset},
		{name: "empty", given: "", want: InitSourceUnknown},
		{name: "unrecognized", given: "conversations", want: InitSourceUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := NewInitSource(tt.given); got != tt.want {
				t.Fatalf("NewInitSource(%q) = %q, want %q", tt.given, got, tt.want)
			}
		})
	}
}
