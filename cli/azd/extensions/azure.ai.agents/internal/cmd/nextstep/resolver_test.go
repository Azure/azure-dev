// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveAfterInit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		state             *State
		wantPrimaryHas    string
		wantManualVarKeys []string
		wantTrailing      string
	}{
		{
			name:           "happy path → run locally",
			state:          &State{},
			wantPrimaryHas: "azd ai agent run",
			wantTrailing:   "azd deploy",
		},
		{
			name:           "infra vars missing → provision",
			state:          &State{MissingInfraVars: []string{"AZURE_AI_FOO"}},
			wantPrimaryHas: "azd provision",
			wantTrailing:   "azd deploy",
		},
		{
			name: "manual vars missing → up to 3 env set lines, sorted",
			state: &State{
				MissingManualVars: []string{"DELTA", "ALPHA", "ECHO", "BRAVO"},
			},
			wantManualVarKeys: []string{"ALPHA", "BRAVO", "DELTA"},
			wantTrailing:      "azd deploy",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := ResolveAfterInit(tt.state)
			require.NotEmpty(t, out)

			// The trailing line is always present and flagged Trailing so
			// the renderer reserves a slot for it during truncation.
			last := out[len(out)-1]
			assert.Equal(t, tt.wantTrailing, last.Command)
			assert.True(t, last.Trailing, "last suggestion must be flagged Trailing")

			if len(tt.wantManualVarKeys) > 0 {
				assert.Len(t, out, len(tt.wantManualVarKeys)+1)
				for i, key := range tt.wantManualVarKeys {
					assert.True(t,
						strings.HasPrefix(out[i].Command, "azd env set "+key+" "),
						"got %q", out[i].Command)
				}
			} else {
				assert.Contains(t, out[0].Command, tt.wantPrimaryHas)
			}
		})
	}
}

func TestResolveAfterInit_ManualVarsCapAtThree(t *testing.T) {
	t.Parallel()

	state := &State{MissingManualVars: []string{"V1", "V2", "V3", "V4", "V5"}}
	out := ResolveAfterInit(state)
	// 3 manual + 1 trailing.
	require.Len(t, out, 4)
	assert.Equal(t, "azd deploy", out[3].Command)
	assert.True(t, out[3].Trailing, "deploy footer must be Trailing")
}

func TestResolveAfterInit_NilState(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ResolveAfterInit(nil))
}

func TestResolveAfterRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		state       *State
		serviceName string
		want        []string // expected substrings, one per emitted command
	}{
		{
			name: "OpenAPI payload extracted → invoke with payload, no tip",
			state: &State{
				HasOpenAPI:     true,
				OpenAPIPayload: `{"message":"hello"}`,
				Services:       []ServiceState{{Name: "echo", Protocol: ProtocolInvocations}},
			},
			serviceName: "echo",
			want: []string{
				`azd ai agent invoke --local '{"message":"hello"}'`,
			},
		},
		{
			name: "invocations protocol, no spec → default JSON payload + tip",
			state: &State{
				Services: []ServiceState{{Name: "echo", Protocol: ProtocolInvocations}},
			},
			serviceName: "echo",
			want: []string{
				`azd ai agent invoke --local '{"message": "Hello!"}'`,
				`curl http://localhost:<port>/invocations/docs/openapi.json`,
			},
		},
		{
			name: "responses protocol, no spec → Hello! string + tip",
			state: &State{
				Services: []ServiceState{{Name: "echo", Protocol: ProtocolResponses}},
			},
			serviceName: "echo",
			want: []string{
				`azd ai agent invoke --local "Hello!"`,
				`curl http://localhost:<port>/invocations/docs/openapi.json`,
			},
		},
		{
			name: "unknown protocol falls back to responses default",
			state: &State{
				Services: []ServiceState{{Name: "echo", Protocol: ""}},
			},
			serviceName: "echo",
			want: []string{
				`azd ai agent invoke --local "Hello!"`,
				`curl http://localhost:<port>/invocations/docs/openapi.json`,
			},
		},
		{
			name: "service name omitted, single-service project picks that one",
			state: &State{
				Services: []ServiceState{{Name: "only", Protocol: ProtocolInvocations}},
			},
			serviceName: "",
			want: []string{
				`azd ai agent invoke --local '{"message": "Hello!"}'`,
				`curl http://localhost:<port>/invocations/docs/openapi.json`,
			},
		},
		{
			name: "OpenAPI payload with apostrophe → POSIX-escaped wrap, no tip",
			state: &State{
				HasOpenAPI:     true,
				OpenAPIPayload: `{"q":"don't"}`,
				Services:       []ServiceState{{Name: "echo", Protocol: ProtocolInvocations}},
			},
			serviceName: "echo",
			want: []string{
				`azd ai agent invoke --local '{"q":"don'\''t"}'`,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := ResolveAfterRun(tt.state, tt.serviceName)
			require.Len(t, out, len(tt.want))
			for i, snippet := range tt.want {
				assert.Contains(t, out[i].Command, snippet)
			}
		})
	}
}

func TestResolveAfterRun_NilState(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ResolveAfterRun(nil, ""))
}

func TestResolveAfterInvoke_Success(t *testing.T) {
	t.Parallel()

	t.Run("local success → ship it", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterInvoke(&State{}, InvokeLocal, "", nil)
		require.Len(t, out, 1)
		assert.Equal(t, "azd deploy", out[0].Command)
	})

	t.Run("remote success with agent name → show <agent> + monitor", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterInvoke(&State{}, InvokeRemote, "echo", nil)
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent show echo", out[0].Command)
		assert.Equal(t, "azd ai agent monitor --follow", out[1].Command)
	})

	t.Run("remote success without agent name → show only", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterInvoke(&State{}, InvokeRemote, "", nil)
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent show", out[0].Command)
	})
}

func TestResolveAfterInvoke_Failure(t *testing.T) {
	t.Parallel()

	t.Run("local failure → see local server output", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterInvoke(&State{}, InvokeLocal, "", &InvokeFailure{})
		require.Len(t, out, 1)
		assert.Contains(t, out[0].Command, "local server output")
	})

	t.Run("remote failure, no session code → generic monitor", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterInvoke(&State{}, InvokeRemote, "echo", &InvokeFailure{})
		require.Len(t, out, 1)
		assert.Equal(t, "azd ai agent monitor --tail 100", out[0].Command)
	})

	t.Run("remote failure with classified code → branched remediation", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterInvoke(&State{}, InvokeRemote, "echo", &InvokeFailure{
			SessionCode: SessionQuotaExceeded,
		})
		require.Len(t, out, 1)
		assert.Equal(t, "azd ai agent session list", out[0].Command)
	})

	t.Run("remote failure with secondary action → both lines, ordered priority", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterInvoke(&State{}, InvokeRemote, "echo", &InvokeFailure{
			SessionCode: SessionReadinessTimeout,
		})
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent invoke", out[0].Command)
		assert.Less(t, out[0].Priority, out[1].Priority)
	})

	t.Run("unrecognized session code → fallback with code in description", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterInvoke(&State{}, InvokeRemote, "echo", &InvokeFailure{
			SessionCode: SessionErrorCode("MysteryCode"),
		})
		require.Len(t, out, 1)
		assert.Contains(t, out[0].Description, "MysteryCode")
	})
}

func TestResolveAfterShow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     AgentVersionStatus
		agentName  string
		wantCmdHas string
	}{
		{"Active without service in state → responses payload", AgentVersionActive, "echo", `azd ai agent invoke echo "Hello!"`},
		{"Creating → monitor system", AgentVersionCreating, "echo", "azd ai agent monitor --type system --follow"},
		{"Failed → monitor tail", AgentVersionFailed, "echo", "azd ai agent monitor --tail 100"},
		{"Deleting → redeploy", AgentVersionDeleting, "echo", "azd deploy"},
		{"Deleted → redeploy", AgentVersionDeleted, "echo", "azd deploy"},
		{"empty status → re-check show", "", "echo", "azd ai agent show echo"},
		{"unknown status → re-check show", "Transitioning", "echo", "azd ai agent show echo"},
		{"unknown status without agent name → bare show", "Transitioning", "", "azd ai agent show"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Same-name case: service and agent names align (common when deploy
			// doesn't append a suffix). Divergent-name cases are covered by
			// TestResolveAfterShow_DivergentNames below.
			out := ResolveAfterShow(&State{AgentStatus: string(tt.status)}, tt.agentName, tt.agentName)
			require.NotEmpty(t, out)
			assert.Contains(t, out[0].Command, tt.wantCmdHas)
		})
	}
}

func TestResolveAfterShow_ActiveHonorsServiceProtocol(t *testing.T) {
	t.Parallel()

	t.Run("invocations protocol → JSON payload", func(t *testing.T) {
		t.Parallel()
		state := &State{
			AgentStatus: string(AgentVersionActive),
			Services:    []ServiceState{{Name: "echo", Protocol: ProtocolInvocations}},
		}
		out := ResolveAfterShow(state, "echo", "echo")
		require.Len(t, out, 1)
		assert.Equal(t, `azd ai agent invoke echo '{"message": "Hello!"}'`, out[0].Command)
	})

	t.Run("responses protocol → bare string payload", func(t *testing.T) {
		t.Parallel()
		state := &State{
			AgentStatus: string(AgentVersionActive),
			Services:    []ServiceState{{Name: "echo", Protocol: ProtocolResponses}},
		}
		out := ResolveAfterShow(state, "echo", "echo")
		require.Len(t, out, 1)
		assert.Equal(t, `azd ai agent invoke echo "Hello!"`, out[0].Command)
	})

	t.Run("service name not present in state → responses fallback", func(t *testing.T) {
		t.Parallel()
		state := &State{
			AgentStatus: string(AgentVersionActive),
			Services:    []ServiceState{{Name: "other", Protocol: ProtocolInvocations}},
		}
		out := ResolveAfterShow(state, "echo", "echo")
		require.Len(t, out, 1)
		assert.Equal(t, `azd ai agent invoke echo "Hello!"`, out[0].Command)
	})
}

// TestResolveAfterShow_DivergentNames locks the G1 behavior: when the
// azure.yaml service name and the deployed Foundry agent name differ,
// protocol lookup keys on serviceName but the emitted invoke command
// embeds agentName (because invoke's remote URL path embeds the agent
// name verbatim and Foundry would 404 on the service name).
func TestResolveAfterShow_DivergentNames(t *testing.T) {
	t.Parallel()

	t.Run("Active branch: protocol from service, name from agent", func(t *testing.T) {
		t.Parallel()
		state := &State{
			AgentStatus: string(AgentVersionActive),
			Services:    []ServiceState{{Name: "svc-echo", Protocol: ProtocolInvocations}},
		}
		out := ResolveAfterShow(state, "svc-echo", "echo-suffix-abc123")
		require.Len(t, out, 1)
		assert.Equal(t, `azd ai agent invoke echo-suffix-abc123 '{"message": "Hello!"}'`, out[0].Command)
	})

	t.Run("unknown status: re-check uses serviceName", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterShow(&State{AgentStatus: "Transitioning"}, "svc-echo", "echo-suffix-abc123")
		require.Len(t, out, 1)
		assert.Equal(t, "azd ai agent show svc-echo", out[0].Command)
	})
}

// TestResolveAfterShow_ActiveConsumesOpenAPICache locks the G2 behavior:
// when state.HasOpenAPI is true and the payload is non-empty, the Active
// suggestion uses the cached payload (shell-escaped) in place of the
// protocol-generic literal so the command matches the agent's actual
// schema.
func TestResolveAfterShow_ActiveConsumesOpenAPICache(t *testing.T) {
	t.Parallel()

	t.Run("cached payload overrides protocol literal", func(t *testing.T) {
		t.Parallel()
		state := &State{
			AgentStatus:    string(AgentVersionActive),
			Services:       []ServiceState{{Name: "echo", Protocol: ProtocolInvocations}},
			HasOpenAPI:     true,
			OpenAPIPayload: `{"prompt": "hi", "max_tokens": 32}`,
		}
		out := ResolveAfterShow(state, "echo", "echo")
		require.Len(t, out, 1)
		assert.Equal(t,
			`azd ai agent invoke echo '{"prompt": "hi", "max_tokens": 32}'`,
			out[0].Command)
	})

	t.Run("payload with apostrophe is POSIX-escaped", func(t *testing.T) {
		t.Parallel()
		state := &State{
			AgentStatus:    string(AgentVersionActive),
			Services:       []ServiceState{{Name: "echo", Protocol: ProtocolInvocations}},
			HasOpenAPI:     true,
			OpenAPIPayload: `{"greeting": "it's me"}`,
		}
		out := ResolveAfterShow(state, "echo", "echo")
		require.Len(t, out, 1)
		assert.Equal(t,
			`azd ai agent invoke echo '{"greeting": "it'\''s me"}'`,
			out[0].Command)
	})

	t.Run("HasOpenAPI true but empty payload falls back to protocol literal", func(t *testing.T) {
		t.Parallel()
		state := &State{
			AgentStatus:    string(AgentVersionActive),
			Services:       []ServiceState{{Name: "echo", Protocol: ProtocolInvocations}},
			HasOpenAPI:     true,
			OpenAPIPayload: "",
		}
		out := ResolveAfterShow(state, "echo", "echo")
		require.Len(t, out, 1)
		assert.Equal(t, `azd ai agent invoke echo '{"message": "Hello!"}'`, out[0].Command)
	})
}

func TestResolveAfterShow_NilState(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ResolveAfterShow(nil, "echo", "echo"))
}

func TestResolveAfterDeploy(t *testing.T) {
	t.Parallel()

	t.Run("single agent, cached payload available → 2 lines, no README hint", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{{Name: "echo", RelativePath: "./src/echo"}}}
		cached := func(_ string) string { return `{"q":"x"}` }
		out := ResolveAfterDeploy(state, cached, nil)
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent show", out[0].Command)
		assert.Equal(t, `azd ai agent invoke '{"q":"x"}'`, out[1].Command)
	})

	t.Run("single agent, no cached payload, README on disk → 3 lines with README pointer", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{{Name: "echo", RelativePath: "./src/echo", Protocol: ProtocolResponses}}}
		readme := func(p string) bool { return p == "./src/echo" }
		out := ResolveAfterDeploy(state, nil, readme)
		require.Len(t, out, 3)
		assert.Equal(t, "azd ai agent show", out[0].Command)
		assert.Equal(t, `azd ai agent invoke "Hello!"`, out[1].Command)
		assert.Contains(t, out[2].Command, "src/echo/README.md")
	})

	t.Run("multi-agent → one show/invoke pair per agent, named", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{
			{Name: "alpha", Protocol: ProtocolInvocations},
			{Name: "beta", Protocol: ProtocolResponses},
		}}
		out := ResolveAfterDeploy(state, nil, nil)
		require.Len(t, out, 4)
		assert.Equal(t, "azd ai agent show alpha", out[0].Command)
		assert.Equal(t, `azd ai agent invoke alpha '{"message": "Hello!"}'`, out[1].Command)
		assert.Equal(t, "azd ai agent show beta", out[2].Command)
		assert.Equal(t, `azd ai agent invoke beta "Hello!"`, out[3].Command)
	})

	t.Run("README hint skipped when cached payload is present", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{{Name: "echo", RelativePath: "./src/echo"}}}
		cached := func(_ string) string { return `{"q":"x"}` }
		readme := func(_ string) bool { return true }
		out := ResolveAfterDeploy(state, cached, readme)
		assert.Len(t, out, 2)
	})

	t.Run("no services → nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, ResolveAfterDeploy(&State{}, nil, nil))
	})

	t.Run("nil state → nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, ResolveAfterDeploy(nil, nil, nil))
	})

	t.Run("cached payload containing apostrophe → POSIX-escaped", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{{Name: "echo", RelativePath: "./src/echo"}}}
		cached := func(_ string) string { return `{"q":"don't"}` }
		out := ResolveAfterDeploy(state, cached, nil)
		require.Len(t, out, 2)
		assert.Equal(t, `azd ai agent invoke '{"q":"don'\''t"}'`, out[1].Command)
	})
}

func TestFindService(t *testing.T) {
	t.Parallel()

	state := &State{Services: []ServiceState{
		{Name: "alpha"},
		{Name: "beta"},
	}}

	assert.Equal(t, "alpha", findService(state, "alpha").Name)
	assert.Equal(t, "beta", findService(state, "beta").Name)
	assert.Nil(t, findService(state, "missing"))

	// Empty name + single service → that one.
	single := &State{Services: []ServiceState{{Name: "only"}}}
	assert.Equal(t, "only", findService(single, "").Name)

	// Empty name + multiple → nil.
	assert.Nil(t, findService(state, ""))

	// Nil state.
	assert.Nil(t, findService(nil, "alpha"))
}
