// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"maps"
	"testing"
)

func TestAgentInvokeSelectedWireContract(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		protocol    string
		longRunning bool
		noWait      bool
		want        map[string]string
	}{
		{"responses foreground", "responses", false, false,
			map[string]string{
				"agent.invoke.protocol": "responses", "agent.invoke.long_running": "false", "agent.invoke.no_wait": "false"}},
		{"responses attached", "responses", true, false,
			map[string]string{
				"agent.invoke.protocol": "responses", "agent.invoke.long_running": "true", "agent.invoke.no_wait": "false"}},
		{"responses detached", "responses", true, true,
			map[string]string{
				"agent.invoke.protocol": "responses", "agent.invoke.long_running": "true", "agent.invoke.no_wait": "true"}},
		{"invocations", "invocations", false, false,
			map[string]string{
				"agent.invoke.protocol": "invocations", "agent.invoke.long_running": "false", "agent.invoke.no_wait": "false"}},
		{"a2a", "a2a", false, false,
			map[string]string{
				"agent.invoke.protocol": "a2a", "agent.invoke.long_running": "false", "agent.invoke.no_wait": "false"}},
		{"future invocable protocol", "future_protocol", false, false,
			map[string]string{
				"agent.invoke.protocol": "future_protocol", "agent.invoke.long_running": "false", "agent.invoke.no_wait": "false"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			event := AgentInvokeSelected(tt.protocol, tt.longRunning, tt.noWait)
			if event.Name != "agent.invoke.selected" {
				t.Fatalf("event name = %q, want agent.invoke.selected", event.Name)
			}
			if !maps.Equal(event.Attributes, tt.want) {
				t.Fatalf("event attributes = %#v, want %#v", event.Attributes, tt.want)
			}
		})
	}
}

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
