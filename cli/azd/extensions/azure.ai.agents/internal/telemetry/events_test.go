// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"maps"
	"testing"
)

func TestLocalClientRouteSelectedWireContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		route     LocalClientRoute
		wantValue string
	}{
		{
			name:      "inspector",
			route:     LocalClientRouteInspector,
			wantValue: "inspector",
		},
		{
			name:      "playground",
			route:     LocalClientRoutePlayground,
			wantValue: "playground",
		},
		{
			name:      "suppressed",
			route:     LocalClientRouteSuppressed,
			wantValue: "suppressed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := LocalClientRouteSelected(tt.route)
			if event.Name != "local_client.route.selected" {
				t.Fatalf("event name = %q, want %q", event.Name, "local_client.route.selected")
			}

			wantAttributes := map[string]string{"route": tt.wantValue}
			if !maps.Equal(event.Attributes, wantAttributes) {
				t.Fatalf("event attributes = %#v, want %#v", event.Attributes, wantAttributes)
			}
		})
	}
}
