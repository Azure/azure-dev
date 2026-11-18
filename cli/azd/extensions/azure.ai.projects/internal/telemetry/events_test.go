// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS

package telemetry

import (
	"maps"
	"testing"
)

func TestAgentHostingSelectedWireContract(t *testing.T) {
	t.Parallel()

	event := AgentHostingSelected(AgentHostingModeBYOAKS)
	if event.Name != "agent.hosting.selected" {
		t.Fatalf("event name = %q, want %q", event.Name, "agent.hosting.selected")
	}

	wantAttributes := map[string]string{"agent.hosting.mode": "byo_aks"}
	if !maps.Equal(event.Attributes, wantAttributes) {
		t.Fatalf("event attributes = %#v, want %#v", event.Attributes, wantAttributes)
	}
}
