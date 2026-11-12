// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"maps"
	"testing"
)

func TestDatasetPublishedWireContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		given     string
		wantValue string
	}{
		{name: "create", given: string(OperationCreate), wantValue: "create"},
		{name: "update", given: string(OperationUpdate), wantValue: "update"},
		{name: "unknown", given: string(OperationUnknown), wantValue: "unknown"},

		// Values that never went through NewOperation. The builder is the
		// boundary the attribute's closed set is guaranteed at, so reaching it
		// directly must still leave one of the three reviewed values.
		{name: "a verb nobody reviewed", given: "publish", wantValue: "unknown"},
		{name: "nothing at all", given: "", wantValue: "unknown"},
		{name: "content a caller could pass by mistake", given: "my-customer-dataset", wantValue: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := DatasetPublished(tt.given)
			if event.Name != "dataset.published" {
				t.Fatalf("event name = %q, want %q", event.Name, "dataset.published")
			}

			wantAttributes := map[string]string{"operation": tt.wantValue}
			if !maps.Equal(event.Attributes, wantAttributes) {
				t.Fatalf("event attributes = %#v, want %#v", event.Attributes, wantAttributes)
			}
		})
	}
}

// A verb this package does not know must not reach the wire as itself, or the
// attribute stops being a closed set the moment the write commands grow a third.
func TestNewOperationClosesOverUnknownValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		given string
		want  Operation
	}{
		{name: "create", given: "create", want: OperationCreate},
		{name: "update", given: "update", want: OperationUpdate},
		{name: "empty", given: "", want: OperationUnknown},
		{name: "unrecognized", given: "publish", want: OperationUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := NewOperation(tt.given); got != tt.want {
				t.Fatalf("NewOperation(%q) = %q, want %q", tt.given, got, tt.want)
			}
		})
	}
}
