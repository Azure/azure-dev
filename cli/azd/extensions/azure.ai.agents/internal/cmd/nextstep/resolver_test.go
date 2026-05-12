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
			name:           "happy path (provisioned) → run locally",
			state:          &State{HasProjectEndpoint: true},
			wantPrimaryHas: "azd ai agent run",
			wantTrailing:   "azd deploy",
		},
		{
			name:           "project endpoint not yet set → provision",
			state:          &State{},
			wantPrimaryHas: "azd provision",
			wantTrailing:   "azd deploy",
		},
		{
			name: "infra vars missing post-provision → provision (re-provision)",
			state: &State{
				HasProjectEndpoint: true,
				MissingInfraVars:   []string{"AZURE_AI_FOO"},
			},
			wantPrimaryHas: "azd provision",
			wantTrailing:   "azd deploy",
		},
		{
			name: "manual vars missing → up to 3 env set lines, sorted",
			state: &State{
				HasProjectEndpoint: true,
				MissingManualVars:  []string{"DELTA", "ALPHA", "ECHO", "BRAVO"},
			},
			wantManualVarKeys: []string{"ALPHA", "BRAVO", "DELTA"},
			wantTrailing:      "azd deploy",
		},
		{
			name: "project endpoint missing wins over manual vars (provision unblocks both)",
			state: &State{
				MissingManualVars: []string{"USER_API_KEY"},
			},
			wantPrimaryHas: "azd provision",
			wantTrailing:   "azd deploy",
		},
		{
			// User selected "Deploy new model(s)" in init. The Foundry
			// project does not exist yet, but a stale
			// AZURE_AI_PROJECT_ENDPOINT carried over from a prior init
			// or sibling environment sets HasProjectEndpoint=true.
			// Without the explicit NeedsAIProjectProvision signal the
			// resolver would default to `azd ai agent run` and
			// mislead the user into running a local invoke against a
			// project that has not been provisioned.
			name: "deploy-new chosen but stale endpoint → provision (override)",
			state: &State{
				HasProjectEndpoint:      true,
				NeedsAIProjectProvision: true,
			},
			wantPrimaryHas: "azd provision",
			wantTrailing:   "azd deploy",
		},
		{
			// Existing-project init path. USE_EXISTING_AI_PROJECT=true
			// leaves NeedsAIProjectProvision=false at state assembly,
			// so the legacy heuristic continues to drive: endpoint
			// set + no missing vars ⇒ `azd ai agent run`. This case
			// locks the no-regression contract for the existing
			// path.
			name: "existing project chosen, all vars set → run locally (no override)",
			state: &State{
				HasProjectEndpoint:      true,
				NeedsAIProjectProvision: false,
			},
			wantPrimaryHas: "azd ai agent run",
			wantTrailing:   "azd deploy",
		},
		{
			// Init configured a new model deployment in an existing
			// Foundry project: HasProjectEndpoint=true (existing
			// project), NeedsAIProjectProvision=false (existing
			// project), but PendingProvisionReasons contains
			// "model_deployment". The resolver must still suggest
			// `azd provision` so Bicep creates the new deployment.
			name: "new model deployment in existing project → provision (PendingProvisionReasons override)",
			state: &State{
				HasProjectEndpoint:      true,
				NeedsAIProjectProvision: false,
				PendingProvisionReasons: []string{"model_deployment"},
			},
			wantPrimaryHas: "azd provision",
			wantTrailing:   "azd deploy",
		},
		{
			// Multiple pending reasons collected during init —
			// e.g. user left ACR blank and configured a new model.
			// Still single `azd provision` suggestion (resolver
			// treats the list as opaque non-emptiness).
			name: "multiple pending reasons → single provision suggestion",
			state: &State{
				HasProjectEndpoint:      true,
				PendingProvisionReasons: []string{"acr", "model_deployment"},
			},
			wantPrimaryHas: "azd provision",
			wantTrailing:   "azd deploy",
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

	state := &State{
		HasProjectEndpoint: true,
		MissingManualVars:  []string{"V1", "V2", "V3", "V4", "V5"},
	}
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

// TestResolveAfterInit_ToolboxReproRendersAllCategories locks the full
// regression for the toolbox-sample bug end-to-end: the state contains
// BOTH an unresolved manifest placeholder AND a missing manual env var,
// and the rendered "Next:" block must surface both fix-up categories
// plus the trailing `azd deploy` reminder. PrintNext would silently
// drop one category here because of its 2-line cap; PrintAllNext must
// not.
func TestResolveAfterInit_ToolboxReproRendersAllCategories(t *testing.T) {
	t.Parallel()

	state := &State{
		HasProjectEndpoint:     true,
		UnresolvedPlaceholders: []string{"TOOLBOX_ENDPOINT"},
		MissingManualVars:      []string{"TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT"},
	}

	var buf strings.Builder
	require.NoError(t, PrintAllNext(&buf, ResolveAfterInit(state)))
	rendered := buf.String()

	assert.Contains(t, rendered,
		"edit agent.yaml: replace {{TOOLBOX_ENDPOINT}} with the actual value",
		"placeholder fix-up missing")
	assert.Contains(t, rendered,
		"azd env set TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT <value>",
		"manual-var fix-up missing — this is the original toolbox-sample regression")
	assert.Contains(t, rendered, "azd deploy", "trailing deploy reminder missing")
}

func TestResolveAfterInit_UnresolvedPlaceholders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		state            *State
		wantPlaceholders []string // expected `{{NAME}}` names in order
		wantMiddle       string   // expected non-trailing, non-placeholder primary (e.g., "azd provision", "azd env set FOO", or "" if none)
		wantHasRun       bool     // expect `azd ai agent run` to appear?
		wantHasDeploy    bool     // expect `azd deploy` trailing?
	}{
		{
			name: "placeholders alone → edit lines + deploy, no run",
			state: &State{
				HasProjectEndpoint:     true,
				UnresolvedPlaceholders: []string{"TOOLBOX_ENDPOINT"},
			},
			wantPlaceholders: []string{"TOOLBOX_ENDPOINT"},
			wantHasRun:       false,
			wantHasDeploy:    true,
		},
		{
			name: "placeholders + missing manual vars → both surfaced, no run",
			state: &State{
				HasProjectEndpoint:     true,
				UnresolvedPlaceholders: []string{"TOOLBOX_ENDPOINT"},
				MissingManualVars:      []string{"TOOLBOX_MCP_ENDPOINT"},
			},
			wantPlaceholders: []string{"TOOLBOX_ENDPOINT"},
			wantMiddle:       "azd env set TOOLBOX_MCP_ENDPOINT",
			wantHasRun:       false,
			wantHasDeploy:    true,
		},
		{
			name: "placeholders + project endpoint missing → placeholders + provision",
			state: &State{
				HasProjectEndpoint:     false,
				UnresolvedPlaceholders: []string{"TOOLBOX_ENDPOINT"},
			},
			wantPlaceholders: []string{"TOOLBOX_ENDPOINT"},
			wantMiddle:       "azd provision",
			wantHasRun:       false,
			wantHasDeploy:    true,
		},
		{
			name: "multiple placeholders sorted ascending",
			state: &State{
				HasProjectEndpoint:     true,
				UnresolvedPlaceholders: []string{"CHARLIE", "ALPHA", "BRAVO"},
			},
			wantPlaceholders: []string{"ALPHA", "BRAVO", "CHARLIE"},
			wantHasRun:       false,
			wantHasDeploy:    true,
		},
		{
			name: "more than three placeholders capped at three",
			state: &State{
				HasProjectEndpoint:     true,
				UnresolvedPlaceholders: []string{"P1", "P2", "P3", "P4", "P5"},
			},
			wantPlaceholders: []string{"P1", "P2", "P3"},
			wantHasRun:       false,
			wantHasDeploy:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := ResolveAfterInit(tt.state)
			require.NotEmpty(t, out)

			// Walk the output:
			//   1. leading run of placeholder fix-ups (one per wantPlaceholders[i])
			//   2. optional middle command (provision / env set)
			//   3. optional `azd ai agent run`
			//   4. trailing `azd deploy`
			for i, name := range tt.wantPlaceholders {
				require.Less(t, i, len(out))
				assert.Equal(t,
					"edit agent.yaml: replace {{"+name+"}} with the actual value",
					out[i].Command,
				)
			}

			// The middle (if any) sits just past the placeholders.
			if tt.wantMiddle != "" {
				idx := len(tt.wantPlaceholders)
				require.Less(t, idx, len(out))
				assert.True(t,
					strings.HasPrefix(out[idx].Command, tt.wantMiddle),
					"middle suggestion %q does not have prefix %q",
					out[idx].Command, tt.wantMiddle,
				)
			}

			hasRun := false
			hasDeploy := false
			for _, s := range out {
				switch {
				case s.Command == "azd ai agent run":
					hasRun = true
				case s.Command == "azd deploy" && s.Trailing:
					hasDeploy = true
				}
			}
			assert.Equal(t, tt.wantHasRun, hasRun,
				"presence of `azd ai agent run` mismatched")
			assert.Equal(t, tt.wantHasDeploy, hasDeploy,
				"presence of trailing `azd deploy` mismatched")
		})
	}
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
			// doesn't append a suffix). Divergent-name behavior is exercised by
			// TestResolveAfterShow_DivergentNames below — the resolver always
			// emits the service name; invoke.go translates to the deployed
			// agent name internally.
			out := ResolveAfterShow(&State{AgentStatus: string(tt.status)}, tt.agentName)
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
		out := ResolveAfterShow(state, "echo")
		require.Len(t, out, 1)
		assert.Equal(t, `azd ai agent invoke echo '{"message": "Hello!"}'`, out[0].Command)
	})

	t.Run("responses protocol → bare string payload", func(t *testing.T) {
		t.Parallel()
		state := &State{
			AgentStatus: string(AgentVersionActive),
			Services:    []ServiceState{{Name: "echo", Protocol: ProtocolResponses}},
		}
		out := ResolveAfterShow(state, "echo")
		require.Len(t, out, 1)
		assert.Equal(t, `azd ai agent invoke echo "Hello!"`, out[0].Command)
	})

	t.Run("service name not present in state → responses fallback", func(t *testing.T) {
		t.Parallel()
		state := &State{
			AgentStatus: string(AgentVersionActive),
			Services:    []ServiceState{{Name: "other", Protocol: ProtocolInvocations}},
		}
		out := ResolveAfterShow(state, "echo")
		require.Len(t, out, 1)
		assert.Equal(t, `azd ai agent invoke echo "Hello!"`, out[0].Command)
	})
}

// TestResolveAfterShow_DivergentNames locks the divergent-name contract:
// when the azure.yaml service name and the deployed Foundry agent name
// differ, the emitted invoke suggestion always uses the SERVICE name as
// the positional. invoke's own protocol/service resolution keys on
// service names, and its invocationsRemote/responsesRemote gates then
// translate to the deployed agent name before constructing the Foundry
// URL. Emitting the deployed name here would fail upstream at
// resolveAgentProtocol with "no azure.ai.agent service named …".
func TestResolveAfterShow_DivergentNames(t *testing.T) {
	t.Parallel()

	t.Run("Active branch: command uses service name (not deployed agent name)", func(t *testing.T) {
		t.Parallel()
		state := &State{
			AgentStatus: string(AgentVersionActive),
			Services:    []ServiceState{{Name: "svc-echo", Protocol: ProtocolInvocations}},
		}
		out := ResolveAfterShow(state, "svc-echo")
		require.Len(t, out, 1)
		assert.Equal(t, `azd ai agent invoke svc-echo '{"message": "Hello!"}'`, out[0].Command)
	})

	t.Run("unknown status: re-check uses service name", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterShow(&State{AgentStatus: "Transitioning"}, "svc-echo")
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
		out := ResolveAfterShow(state, "echo")
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
		out := ResolveAfterShow(state, "echo")
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
		out := ResolveAfterShow(state, "echo")
		require.Len(t, out, 1)
		assert.Equal(t, `azd ai agent invoke echo '{"message": "Hello!"}'`, out[0].Command)
	})
}

func TestResolveAfterShow_NilState(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ResolveAfterShow(nil, "echo"))
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

	t.Run("ForceQualified=true on len==1 → service-qualified commands", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{
			{Name: "echo", RelativePath: "./src/echo", Protocol: ProtocolInvocations},
		}}
		out := ResolveAfterDeploy(state, nil, nil, AfterDeployOpts{ForceQualified: true})
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent show echo", out[0].Command)
		assert.Equal(t, `azd ai agent invoke echo '{"message": "Hello!"}'`, out[1].Command)
	})

	t.Run("ForceQualified=false on len==1 → unqualified (matches default)", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{
			{Name: "echo", RelativePath: "./src/echo", Protocol: ProtocolInvocations},
		}}
		out := ResolveAfterDeploy(state, nil, nil, AfterDeployOpts{ForceQualified: false})
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent show", out[0].Command)
		assert.Equal(t, `azd ai agent invoke '{"message": "Hello!"}'`, out[1].Command)
	})

	t.Run("ForceQualified=true with cached payload → qualified invoke uses payload", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{{Name: "echo", RelativePath: "./src/echo"}}}
		cached := func(_ string) string { return `{"q":"x"}` }
		out := ResolveAfterDeploy(state, cached, nil, AfterDeployOpts{ForceQualified: true})
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent show echo", out[0].Command)
		assert.Equal(t, `azd ai agent invoke echo '{"q":"x"}'`, out[1].Command)
	})

	t.Run("ForceQualified=true on multi-agent → qualified (already-qualified case unaffected)", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{
			{Name: "alpha", Protocol: ProtocolInvocations},
			{Name: "beta", Protocol: ProtocolResponses},
		}}
		out := ResolveAfterDeploy(state, nil, nil, AfterDeployOpts{ForceQualified: true})
		require.Len(t, out, 4)
		assert.Equal(t, "azd ai agent show alpha", out[0].Command)
		assert.Equal(t, `azd ai agent invoke alpha '{"message": "Hello!"}'`, out[1].Command)
		assert.Equal(t, "azd ai agent show beta", out[2].Command)
		assert.Equal(t, `azd ai agent invoke beta "Hello!"`, out[3].Command)
	})

	t.Run("extra opts elements beyond [0] are ignored", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{
			{Name: "echo", RelativePath: "./src/echo", Protocol: ProtocolInvocations},
		}}
		out := ResolveAfterDeploy(
			state, nil, nil,
			AfterDeployOpts{ForceQualified: true},
			AfterDeployOpts{ForceQualified: false}, // should be ignored
		)
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent show echo", out[0].Command)
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
