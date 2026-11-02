// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"strings"
	"testing"

	"azureaiagent/internal/featureflags"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestMultiServiceLocalInvokeNextSteps(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		protocols []string
		wantLocal bool
	}{
		{"two A2A services", []string{ProtocolA2A, ProtocolA2A}, true},
		{"A2A and Responses", []string{ProtocolA2A, ProtocolResponses}, true},
		{"A2A and unknown", []string{ProtocolA2A, ""}, true},
		{"A2A and WebSocket", []string{ProtocolA2A, ProtocolInvocationsWS}, true},
		{"no services known", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := &State{HasProjectEndpoint: true}
			for i, protocol := range tc.protocols {
				state.Services = append(state.Services, ServiceState{
					Name: []string{"first", "second"}[i], Protocol: protocol,
				})
			}
			// Both init branches share the local secondary; neither should
			// suggest an invocation that every configured service rejects.
			for _, missing := range [][]string{nil, {"API_KEY"}} {
				state.MissingManualVars = missing
				foundLocal := false
				for _, suggestion := range ResolveAfterInit(state, nil) {
					foundLocal = foundLocal || strings.Contains(suggestion.Command, "invoke --local")
				}
				require.Equal(t, tc.wantLocal, foundLocal)
			}
			// Local capability filtering must not remove remote A2A guidance.
			remote := ResolveAfterDeploy(state, nil, nil)
			for i, service := range state.Services {
				if service.Protocol == ProtocolA2A || service.Protocol == ProtocolResponses || service.Protocol == "" {
					require.Contains(t, remote, Suggestion{
						Command:     "azd ai agent invoke " + service.Name + " '<payload>'",
						Description: "test " + service.Name,
						Priority:    12 + i,
					})
				}
			}
		})
	}
}

func TestA2AOnlyNextStepsSuggestLocalInvoke(t *testing.T) {
	t.Parallel()
	properties, err := structpb.NewStruct(map[string]any{
		"kind": "hosted", "name": "agent",
		"protocols": []any{map[string]any{"protocol": "a2a", "version": "1.0.0"}},
	})
	require.NoError(t, err)
	protocol, multi := loadServiceProtocolInfo(t.TempDir(), &azdext.ServiceConfig{AdditionalProperties: properties})
	require.Equal(t, ProtocolA2A, protocol)
	state := &State{HasProjectEndpoint: true, Services: []ServiceState{{
		Name: "agent", Protocol: protocol, MultiProtocol: multi,
	}}}
	require.Contains(t, ResolveAfterInit(state, nil), Suggestion{
		Command:     "azd ai agent invoke --local --protocol a2a 'Hello!'",
		Description: "test it in another terminal", Priority: 6,
	})
	require.Equal(t, []Suggestion{{
		Command:     "azd ai agent invoke 'agent' --local --protocol a2a 'Hello!'",
		Description: "send a sample request to the running agent", Priority: 10,
	}}, ResolveAfterRun(state, "agent", nil))
	remote := ResolveAfterDeploy(state, nil, nil)
	require.Contains(t, remote, Suggestion{
		Command: "azd ai agent invoke agent '<payload>'", Description: "test the deployment", Priority: 11,
	})

	// A separate local-capable implementation still permits local guidance.
	protocol, multi = loadServiceProtocolFromBytes([]byte("protocols:\n" +
		"  - protocol: a2a\n    version: 1.0.0\n  - protocol: responses\n    version: 2.0.0\n"))
	state.Services[0].Protocol, state.Services[0].MultiProtocol = protocol, multi
	require.Equal(t, ProtocolResponses, protocol)
	foundLocal := false
	for _, suggestion := range ResolveAfterInit(state, nil) {
		foundLocal = foundLocal || strings.Contains(suggestion.Command, "invoke --local")
	}
	require.True(t, foundLocal)
}

func TestLocalA2AGuidancePortAndPreview(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		service    string
		serviceArg string
		port       int
		preview    bool
		want       string
	}{
		{"legacy default", "agent", "'agent'", 0, false, "'Hello!'"},
		{"legacy custom", "agent", "'agent'", 9090, false, "--port 9090 'Hello!'"},
		{"preview default", "agent", "'agent'", 0, true, "--a2a-version 1.0 'Hello!'"},
		{"preview custom", "agent", "'agent'", 9091, true, "--port 9091 --a2a-version 1.0 'Hello!'"},
		{"legacy quoted service", "agent's service", `'agent'\''s service'`, 9090, false, "--port 9090 'Hello!'"},
		{"preview quoted service", "agent's service", `'agent'\''s service'`,
			9091, true, "--port 9091 --a2a-version 1.0 'Hello!'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			state := &State{
				LocalPort: tc.port, A2APreviewEnabled: tc.preview, HasProjectEndpoint: true,
				HasOpenAPI: true, OpenAPIPayload: `{"stale":"invocations"}`,
				Services: []ServiceState{
					{Name: "other", Protocol: ProtocolA2A},
					{Name: tc.service, Protocol: ProtocolA2A},
				},
			}
			got := ResolveAfterRun(state, tc.service, func(string) bool { return true })
			require.Equal(t, []Suggestion{{
				Command:     "azd ai agent invoke " + tc.serviceArg + " --local --protocol a2a " + tc.want,
				Description: "send a sample request to the running agent", Priority: 10,
			}}, got)
			// Init has no selected run service, including in its manual-vars branch.
			for _, missing := range [][]string{nil, {"API_KEY"}} {
				state.MissingManualVars = missing
				found := false
				for _, suggestion := range ResolveAfterInit(state, nil) {
					if strings.Contains(suggestion.Command, "invoke") {
						require.Equal(t, "azd ai agent invoke --local --protocol a2a "+tc.want, suggestion.Command)
						found = true
					}
				}
				require.True(t, found)
			}
		})
	}
}

func TestAssembleLocalA2AGuidance(t *testing.T) {
	for _, preview := range []string{"false", "true"} {
		t.Run(preview, func(t *testing.T) {
			t.Setenv(featureflags.A2A.EnvVar, preview)
			properties, err := structpb.NewStruct(map[string]any{
				"kind": "hosted", "name": "agent",
				"protocols": []any{map[string]any{"protocol": "a2a", "version": "1.0.0"}},
			})
			require.NoError(t, err)
			src := &fakeSource{
				envName: "dev",
				project: &azdext.ProjectConfig{
					Path: t.TempDir(),
					Services: map[string]*azdext.ServiceConfig{
						"agent": {Name: "agent", Host: agentHost, AdditionalProperties: properties},
					},
				},
			}
			probeCalled := false
			state, errs := AssembleStateFromSource(t.Context(), src,
				WithLocalPort(9092), WithOpenAPIProbe("agent", "local"),
				WithLiveOpenAPIProbe(func(context.Context) ([]byte, error) {
					probeCalled = true
					return nil, nil
				}))
			require.Empty(t, errs)
			require.False(t, probeCalled, "A2A must not probe the invocations OpenAPI route")
			require.False(t, state.HasOpenAPI)
			require.Equal(t, 9092, state.LocalPort)
			require.Equal(t, preview == "true", state.A2APreviewEnabled)
			// The resolver uses the captured snapshot, not subsequent process changes.
			t.Setenv(featureflags.A2A.EnvVar, "false")
			got := ResolveAfterRun(state, "agent", nil)
			require.Len(t, got, 1)
			require.Contains(t, got[0].Command, "--port 9092")
			require.Equal(t, preview == "true", strings.Contains(got[0].Command, "--a2a-version 1.0"))
		})
	}
}

func TestMixedA2ANextStepsPreferInvocableProtocols(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ other, want string }{
		{ProtocolActivity, ProtocolA2A},
		{ProtocolInvocationsWS, ProtocolA2A},
		{ProtocolResponses, ProtocolResponses},
		{ProtocolInvocations, ProtocolInvocations},
	} {
		for _, order := range [][]string{{ProtocolA2A, tc.other}, {tc.other, ProtocolA2A}} {
			t.Run(strings.Join(order, "+"), func(t *testing.T) {
				t.Parallel()
				var data strings.Builder
				data.WriteString("protocols:\n")
				for _, protocol := range order {
					data.WriteString("  - protocol: " + protocol + "\n    version: 1.0.0\n")
				}
				protocol, multi := loadServiceProtocolFromBytes([]byte(data.String()))
				require.Equal(t, tc.want, protocol)
				require.True(t, multi)
				state := &State{HasProjectEndpoint: true, Services: []ServiceState{{
					Name: "agent", Protocol: protocol, MultiProtocol: multi,
				}}}
				remote := ResolveAfterDeploy(state, nil, nil)
				require.Contains(t, remote, Suggestion{
					Command:     "azd ai agent invoke agent --protocol " + tc.want + " '<payload>'",
					Description: "test the deployment", Priority: 11,
				})
			})
		}
	}
}
