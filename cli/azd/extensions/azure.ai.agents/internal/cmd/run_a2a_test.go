// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/telemetry"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestA2AOnlyRunTargetRouting(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		definition     string
		wantSuppressed bool
	}{
		{"missing", "", false},
		{"empty", "kind: hosted", false},
		{"A2A", "protocols: [{protocol: a2a, version: 1.0.0}]", true},
		{"trimmed", "protocols: [{protocol: ' a2a ', version: 0.3.0}]", true},
		{"Responses", "protocols: [{protocol: responses}]", false},
		{"mixed Responses", "protocols: [{protocol: a2a}, {protocol: responses}]", false},
		{"mixed Invocations", "protocols: [{protocol: invocations}, {protocol: a2a}]", false},
		{"mixed Activity", "protocols: [{protocol: a2a}, {protocol: activity}]", false},
		{"mixed WebSocket", "protocols: [{protocol: a2a}, {protocol: invocations_ws}]", false},
		{"unknown protocol", "protocols: [{protocol: a2a}, {protocol: future}]", false},
		{"endpoint only", "agent_endpoint: {protocols: [a2a]}", false},
		{"activity container A2A endpoint", "protocols: [{protocol: activity}]\nagent_endpoint: {protocols: [a2a]}", false},
		{"mixed endpoint", "protocols: [{protocol: a2a}]\nagent_endpoint: {protocols: [activity]}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var def *agent_yaml.ContainerAgent
			if tc.definition != "" {
				def = &agent_yaml.ContainerAgent{}
				require.NoError(t, yaml.Unmarshal([]byte(tc.definition), def))
			}
			profile, suppressed := resolveRunClientProfile(def)
			require.Equal(t, tc.wantSuppressed, suppressed)
			require.Equal(t, isA2AOnlyRunTarget(def), suppressed)
			if !suppressed {
				// Endpoint-only A2A must not suppress Inspector, and mixed
				// container protocols must preserve the existing client route.
				require.Equal(t, resolveActivityRunProfile(def), profile)
				reporter := &recordingUsageReporter{}
				reportLocalClientRouteSelected(t.Context(), reporter, profile, suppressed)
				require.NotEqual(t, telemetry.LocalClientRouteSelected(telemetry.LocalClientRouteSuppressed), reporter.event)
				return
			}
			require.Zero(t, profile)
			reporter := &recordingUsageReporter{}
			reportLocalClientRouteSelected(t.Context(), reporter, profile, suppressed)
			require.Equal(t, telemetry.LocalClientRouteSelected(telemetry.LocalClientRouteSuppressed), reporter.event)
			require.ErrorContains(t, validateInspectorPortForProfile(&runFlags{
				port: 9090, inspectorPort: 9090, inspectorPortSet: true,
			}, profile.IsActivity, suppressed), "not supported for A2A-only agents")
			require.NoError(t, validateInspectorPortForProfile(&runFlags{port: 9090}, profile.IsActivity, suppressed))
			// A nil workflow would panic if Inspector tried to launch.
			var stderr bytes.Buffer
			handleInspectorAutoLaunch(t.Context(), nil, 9090, 0, suppressed, true, nil, &stderr)
			require.Empty(t, stderr.String())
		})
	}
}

func TestResolvedRunTargetNextSteps(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		services []nextstep.ServiceState
	}{
		{"missing services after reload failure", nil},
		{"missing target", []nextstep.ServiceState{{Name: "other", Protocol: nextstep.ProtocolResponses}}},
		{"conflicting activity", []nextstep.ServiceState{{Name: "agent", Protocol: nextstep.ProtocolActivity}}},
		{"conflicting invocations", []nextstep.ServiceState{{Name: "agent", Protocol: nextstep.ProtocolInvocations, MultiProtocol: true}}},
		{"two A2A services", []nextstep.ServiceState{
			{Name: "other", Protocol: nextstep.ProtocolA2A},
			{Name: "agent", Protocol: nextstep.ProtocolA2A},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := &nextstep.State{Services: tc.services, LocalPort: 9091, HasOpenAPI: true, OpenAPIPayload: `{"stale":true}`}
			original := append([]nextstep.ServiceState(nil), state.Services...)
			resolved := withResolvedRunTarget(state, "agent", true)
			require.Equal(t, original, state.Services, "must not mutate the reloaded services")
			for _, svc := range resolved.Services {
				if svc.Name == "other" {
					require.Equal(t, original[0], svc)
				}
			}
			suggestions := nextstep.ResolveAfterRun(resolved, "agent", func(string) bool { return true })
			require.Len(t, suggestions, 1)
			require.Equal(t, "azd ai agent invoke 'agent' --local --protocol a2a --port 9091 'Hello!'", suggestions[0].Command)
			require.Same(t, state, withResolvedRunTarget(state, "agent", false), "other routes must remain unchanged")
		})
	}
}
