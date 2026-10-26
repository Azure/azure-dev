// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"strings"
	"testing"

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
		{"two A2A services", []string{ProtocolA2A, ProtocolA2A}, false},
		{"A2A and Responses", []string{ProtocolA2A, ProtocolResponses}, true},
		{"A2A and unknown", []string{ProtocolA2A, ""}, true},
		{"A2A and WebSocket", []string{ProtocolA2A, ProtocolInvocationsWS}, false},
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

func TestA2AOnlyNextStepsDoNotSuggestLocalInvoke(t *testing.T) {
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
	for _, suggestions := range [][]Suggestion{
		ResolveAfterInit(state, nil), ResolveAfterRun(state, "agent", nil),
	} {
		for _, suggestion := range suggestions {
			require.NotContains(t, suggestion.Command, "invoke --local")
		}
	}
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
