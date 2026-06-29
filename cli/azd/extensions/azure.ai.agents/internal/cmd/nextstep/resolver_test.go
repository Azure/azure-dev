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
			// FOUNDRY_PROJECT_ENDPOINT carried over from a prior init
			// or sibling environment sets HasProjectEndpoint=true.
			// Without the explicit "project" pending-provision tag
			// the resolver would default to `azd ai agent run` and
			// mislead the user into running a local invoke against a
			// project that has not been provisioned.
			name: "deploy-new chosen but stale endpoint → provision (override)",
			state: &State{
				HasProjectEndpoint:      true,
				PendingProvisionReasons: []string{"project"},
			},
			wantPrimaryHas: "azd provision",
			wantTrailing:   "azd deploy",
		},
		{
			// Existing-project init path. USE_EXISTING_AI_PROJECT=true
			// in the env var leaves PendingProvisionReasons empty at
			// state assembly, so the legacy heuristic continues to
			// drive: endpoint set + no missing vars ⇒ `azd ai agent
			// run`. This case locks the no-regression contract for
			// the existing path.
			name: "existing project chosen, all vars set → run locally (no override)",
			state: &State{
				HasProjectEndpoint: true,
			},
			wantPrimaryHas: "azd ai agent run",
			wantTrailing:   "azd deploy",
		},
		{
			// Init configured a new model deployment in an existing
			// Foundry project: HasProjectEndpoint=true (existing
			// project), but PendingProvisionReasons contains
			// "model_deployment". The resolver must still suggest
			// `azd provision` so Bicep creates the new deployment.
			name: "new model deployment in existing project → provision (PendingProvisionReasons override)",
			state: &State{
				HasProjectEndpoint:      true,
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
		{
			name: "provision needed with missing Azure context → env set before provision",
			state: &State{
				PendingProvisionReasons: []string{"project"},
				MissingAzureContextVars: []string{"AZURE_SUBSCRIPTION_ID", "AZURE_LOCATION"},
			},
			wantPrimaryHas: "azd env set AZURE_SUBSCRIPTION_ID",
			wantTrailing:   "azd deploy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := ResolveAfterInit(tt.state, nil)
			require.NotEmpty(t, out)

			// The trailing line is always present and flagged Trailing so
			// the renderer reserves a slot for it during truncation.
			last := out[len(out)-1]
			assert.Equal(t, tt.wantTrailing, last.Command)
			assert.True(t, last.Trailing, "last suggestion must be flagged Trailing")

			if len(tt.wantManualVarKeys) > 0 {
				// N env-set lines + 1 `azd ai agent run` follow-up +
				// 1 invoke-local secondary + 1 trailing `azd deploy`.
				assert.Len(t, out, len(tt.wantManualVarKeys)+3)
				for i, key := range tt.wantManualVarKeys {
					assert.True(t,
						strings.HasPrefix(out[i].Command, "azd env set "+key+" "),
						"got %q", out[i].Command)
				}
				// The slot immediately after the env-set lines is the
				// run follow-up — see ResolveAfterInit's MissingManualVars
				// branch (issue #7975 manual-vars example).
				followUp := out[len(tt.wantManualVarKeys)]
				assert.Equal(t, "azd ai agent run", followUp.Command,
					"expected `azd ai agent run` follow-up after env-set lines")
				assert.False(t, followUp.Trailing,
					"run follow-up must be a primary suggestion, not Trailing")
				// The slot after the run follow-up is the invoke-local
				// secondary that tells the user how to test the agent
				// once it's running.
				invokeLocal := out[len(tt.wantManualVarKeys)+1]
				assert.True(t,
					strings.HasPrefix(invokeLocal.Command, "azd ai agent invoke --local "),
					"expected invoke-local secondary after run follow-up, got %q",
					invokeLocal.Command)
				assert.False(t, invokeLocal.Trailing,
					"invoke-local secondary must be a primary suggestion, not Trailing")
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
	out := ResolveAfterInit(state, nil)
	// 3 env-set lines (capped) + 1 `azd ai agent run` follow-up +
	// 1 `azd ai agent invoke --local` secondary + 1 trailing `azd deploy`.
	require.Len(t, out, 6)
	for i := range 3 {
		assert.True(t, strings.HasPrefix(out[i].Command, "azd env set "),
			"slot %d should be an env-set line, got %q", i, out[i].Command)
	}
	assert.Equal(t, "azd ai agent run", out[3].Command,
		"slot 3 should be the run follow-up")
	assert.True(t, strings.HasPrefix(out[4].Command, "azd ai agent invoke --local "),
		"slot 4 should be the invoke-local secondary, got %q", out[4].Command)
	assert.Equal(t, "azd deploy", out[5].Command)
	assert.True(t, out[5].Trailing, "deploy footer must be Trailing")
}

func TestResolveAfterInit_MissingAzureContextVarsPrecedeProvision(t *testing.T) {
	t.Parallel()

	state := &State{
		PendingProvisionReasons: []string{"project"},
		MissingAzureContextVars: []string{"AZURE_SUBSCRIPTION_ID", "AZURE_LOCATION"},
	}
	out := ResolveAfterInit(state, nil)
	require.Len(t, out, 4)
	assert.Equal(t, "azd env set AZURE_SUBSCRIPTION_ID <value>", out[0].Command)
	assert.Equal(t, "required before provisioning Azure resources", out[0].Description)
	assert.Equal(t, "azd env set AZURE_LOCATION <value>", out[1].Command)
	assert.Equal(t, "azd provision", out[2].Command)
	assert.Equal(t, "azd deploy", out[3].Command)
	assert.True(t, out[3].Trailing, "deploy footer must be Trailing")
}

func TestResolveAfterInit_NilState(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ResolveAfterInit(nil, nil))
}

func TestResolveAfterInit_CreatedFolder(t *testing.T) {
	t.Parallel()

	t.Run("cd suggestion prepended when folder was created", func(t *testing.T) {
		t.Parallel()
		state := &State{
			HasProjectEndpoint:   true,
			CreatedFolderDisplay: "my-agent",
		}
		out := ResolveAfterInit(state, nil)
		require.NotEmpty(t, out)
		assert.Equal(t, "cd my-agent", out[0].Command)
		assert.Equal(t, "enter your new project folder", out[0].Description)
		assert.Equal(t, 0, out[0].Priority, "cd suggestion should have highest priority")
		// Next primary is run, trailing is deploy
		assert.Contains(t, out[1].Command, "azd ai agent run")
		assert.Equal(t, "azd deploy", out[len(out)-1].Command)
	})

	t.Run("no cd suggestion when no folder created", func(t *testing.T) {
		t.Parallel()
		state := &State{HasProjectEndpoint: true}
		out := ResolveAfterInit(state, nil)
		require.NotEmpty(t, out)
		for _, s := range out {
			assert.False(t, strings.HasPrefix(s.Command, "cd "),
				"should not contain cd suggestion, got %q", s.Command)
		}
	})

	t.Run("cd suggestion before provision when infra missing", func(t *testing.T) {
		t.Parallel()
		state := &State{
			CreatedFolderDisplay: "hello-world",
		}
		out := ResolveAfterInit(state, nil)
		require.True(t, len(out) >= 2)
		assert.Equal(t, "cd hello-world", out[0].Command)
		assert.Equal(t, "azd provision", out[1].Command)
	})
}

// TestResolveAfterInit_ManualVarsSingleEmitsEnrichedShape locks the
// single-missing-manual-var case end-to-end. Three asserts: the env-set
// line has the enriched "referenced by agent.yaml but not set in azd
// env" description, the `azd ai agent run` follow-up immediately follows
// the env-set lines, and the trailing `azd deploy` reminder is preserved.
// This is the canonical B2 fix shape from issue #7975's "Example output
// (project ready, but manual config values missing)".
func TestResolveAfterInit_ManualVarsSingleEmitsEnrichedShape(t *testing.T) {
	t.Parallel()

	state := &State{
		HasProjectEndpoint: true,
		MissingManualVars:  []string{"MY_API_KEY"},
	}
	out := ResolveAfterInit(state, nil)
	// 1 env-set + 1 run follow-up + 1 invoke-local secondary + 1 trailing.
	require.Len(t, out, 4)

	assert.Equal(t, "azd env set MY_API_KEY <value>", out[0].Command)
	assert.Equal(t, "referenced by agent.yaml but not set in azd env", out[0].Description,
		"enriched description must explain WHY the env-set is needed")
	assert.False(t, out[0].Trailing)

	assert.Equal(t, "azd ai agent run", out[1].Command)
	assert.Equal(t, "start the agent locally once the values above are set", out[1].Description)
	assert.False(t, out[1].Trailing, "run follow-up must be a primary suggestion")

	assert.Equal(t, `azd ai agent invoke --local '<payload>'`, out[2].Command)
	assert.Equal(t, "test it in another terminal", out[2].Description)
	assert.False(t, out[2].Trailing, "invoke-local secondary must be a primary suggestion")

	assert.Equal(t, "azd deploy", out[3].Command)
	assert.True(t, out[3].Trailing)
}

// TestResolveAfterInit_EverythingReady_EmitsInvokeLocalSecondary locks
// the spec-mandated two-line "everything ready" shape from issue #7975
// lines 96-103: after `azd ai agent run`, append
// `azd ai agent invoke --local <payload>` so the user knows what to
// try in another terminal. Also verifies protocol-aware payload selection
// (single-service state) and the priority ordering (run before invoke).
func TestResolveAfterInit_EverythingReady_EmitsInvokeLocalSecondary(t *testing.T) {
	t.Parallel()

	t.Run("zero services → unqualified invoke with placeholder payload", func(t *testing.T) {
		t.Parallel()
		state := &State{HasProjectEndpoint: true}
		out := ResolveAfterInit(state, nil)
		// run + invoke --local + trailing.
		require.Len(t, out, 3)
		assert.Equal(t, "azd ai agent run", out[0].Command)
		assert.Equal(t, "start the agent locally", out[0].Description)
		assert.Equal(t, `azd ai agent invoke --local '<payload>'`, out[1].Command)
		assert.Equal(t, "test it in another terminal", out[1].Description)
		assert.Less(t, out[0].Priority, out[1].Priority,
			"run must precede invoke --local; the renderer sorts by Priority")
		assert.Equal(t, "azd deploy", out[2].Command)
		assert.True(t, out[2].Trailing)
	})

	t.Run("single-agent responses protocol → invoke uses placeholder", func(t *testing.T) {
		t.Parallel()
		state := &State{
			HasProjectEndpoint: true,
			Services:           []ServiceState{{Name: "echo", Protocol: ProtocolResponses}},
		}
		out := ResolveAfterInit(state, nil)
		require.Len(t, out, 3)
		assert.Equal(t, "azd ai agent run", out[0].Command)
		assert.Equal(t, `azd ai agent invoke --local '<payload>'`, out[1].Command)
	})

	t.Run("single-agent invocations protocol → invoke uses placeholder", func(t *testing.T) {
		t.Parallel()
		state := &State{
			HasProjectEndpoint: true,
			Services:           []ServiceState{{Name: "echo", Protocol: ProtocolInvocations}},
		}
		out := ResolveAfterInit(state, nil)
		require.Len(t, out, 3)
		assert.Equal(t, "azd ai agent run", out[0].Command)
		assert.Equal(t, `azd ai agent invoke --local '<payload>'`, out[1].Command)
	})

	t.Run("multi-agent → invoke stays unqualified, uses placeholder payload", func(t *testing.T) {
		t.Parallel()
		state := &State{
			HasProjectEndpoint: true,
			Services: []ServiceState{
				{Name: "alpha", Protocol: ProtocolInvocations},
				{Name: "beta", Protocol: ProtocolResponses},
			},
		}
		out := ResolveAfterInit(state, nil)
		require.Len(t, out, 3)
		assert.Equal(t, "azd ai agent run", out[0].Command)
		// Multi-agent: the unqualified `invoke --local` doesn't know
		// which service the user will pick at runtime, so emit the
		// bare '<payload>' placeholder rather than a per-protocol
		// payload that may not match the user's chosen service.
		assert.Equal(t, `azd ai agent invoke --local '<payload>'`, out[1].Command)
	})

	t.Run("placeholders present → invoke-local secondary suppressed (with run)", func(t *testing.T) {
		// Placeholders block local run entirely — the spec's default
		// branch is gated on !hasPlaceholders, so neither `run` nor
		// the invoke-local follow-up should appear when literal
		// {{NAME}} values would land in the running container.
		t.Parallel()
		state := &State{
			HasProjectEndpoint:     true,
			UnresolvedPlaceholders: []string{"FOO"},
		}
		out := ResolveAfterInit(state, nil)
		for _, s := range out {
			assert.NotContains(t, s.Command, "azd ai agent invoke --local",
				"invoke --local must not be emitted while placeholders are unresolved")
			assert.NotEqual(t, "azd ai agent run", s.Command,
				"azd ai agent run must not be emitted while placeholders are unresolved")
		}
	})
}

// TestResolveAfterInit_ToolboxReproRendersAllCategories locks the full
// regression for the toolbox-sample bug end-to-end: the state contains
// BOTH an unresolved manifest placeholder AND a manifest-declared
// toolbox whose azd-injected endpoint env var is unset. The rendered
// "Next:" block must surface the placeholder fix-up AND route the
// missing toolbox endpoint to `azd provision` (NOT to `azd env set`),
// plus the trailing `azd deploy` reminder.
//
// Before #8198's toolbox-endpoint partition this would have rendered
// "azd env set TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT <value>" — see
// the test body's NotContains assertion below for the historical bug
// shape we're locking out.
func TestResolveAfterInit_ToolboxReproRendersAllCategories(t *testing.T) {
	t.Parallel()

	state := &State{
		HasProjectEndpoint:     true,
		UnresolvedPlaceholders: []string{"TOOLBOX_ENDPOINT"},
		MissingToolboxEndpoints: []ResourceRef{
			{Name: "web-search-tools", ServiceName: "agent"},
		},
	}

	var buf strings.Builder
	require.NoError(t, PrintAllNext(&buf, ResolveAfterInit(state, nil)))
	rendered := buf.String()

	assert.Contains(t, rendered,
		"edit azure.yaml: replace {{TOOLBOX_ENDPOINT}} with the actual value",
		"placeholder fix-up missing")
	assert.Contains(t, rendered, "azd provision",
		"toolbox-endpoint branch should route to azd provision")
	assert.Contains(t, rendered, "azd ai agent doctor",
		"toolbox-endpoint branch should surface doctor as an existence-check follow-up")
	// Historical bug: the resolver used to emit `azd env set` for
	// toolbox-derived endpoint vars. Those vars are azd-managed
	// outputs of `azd provision`, not operator-supplied, so the
	// `azd env set` shape is wrong here regardless of the user's
	// `<value>` placeholder gripe.
	assert.NotContains(t, rendered,
		"azd env set TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT",
		"toolbox endpoint var must not be routed through `azd env set`")
	assert.Contains(t, rendered, "azd deploy", "trailing deploy reminder missing")
}

// TestResolveAfterInit_ToolboxEndpointsEmitsRunAndInvokeLocal locks the
// "happy path" for the toolbox-endpoint branch: when the only thing
// blocking the user is a manifest-declared toolbox whose
// TOOLBOX_<NAME>_MCP_ENDPOINT var is unset (no placeholders, no
// pending provision reasons), the resolver must render the full
// post-init sequence:
//
//  1. `azd provision`               — create the toolbox in Foundry
//  2. `azd ai agent doctor`         — optional existence-check follow-up
//  3. `azd ai agent run`            — start locally once provision completes
//  4. `azd ai agent invoke --local` — secondary; test the running agent
//  5. `azd deploy`                  — trailing reminder
//
// Steps 3 and 4 are crucial: once provision finishes, main.py's
// runtime fallback (constructed from FOUNDRY_PROJECT_ENDPOINT +
// TOOLBOX_NAME) satisfies the agent even without the user setting
// the azd-injected endpoint var, so the local-test commands MUST
// appear here just as they do in the MissingManualVars branch.
// Dropping them was the regression this test guards against.
func TestResolveAfterInit_ToolboxEndpointsEmitsRunAndInvokeLocal(t *testing.T) {
	t.Parallel()

	state := &State{
		HasProjectEndpoint: true,
		MissingToolboxEndpoints: []ResourceRef{
			{Name: "web-search-tools", ServiceName: "agent"},
		},
	}

	var buf strings.Builder
	require.NoError(t, PrintAllNext(&buf, ResolveAfterInit(state, nil)))
	rendered := buf.String()

	assert.Contains(t, rendered, "azd provision",
		"toolbox-endpoint branch should route to azd provision")
	assert.Contains(t, rendered, "azd ai agent doctor",
		"toolbox-endpoint branch should surface doctor as an existence-check follow-up")
	assert.Contains(t, rendered, "azd ai agent run",
		"toolbox-endpoint branch should emit local run follow-up once provision completes")
	assert.Contains(t, rendered, "once provision completes",
		"toolbox-only run description must name provision as the prerequisite")
	assert.Contains(t, rendered, "azd ai agent invoke --local",
		"toolbox-endpoint branch should emit invoke-local secondary")
	assert.NotContains(t, rendered,
		"azd env set TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT",
		"toolbox endpoint var must not be routed through `azd env set`")
	assert.Contains(t, rendered, "azd deploy", "trailing deploy reminder missing")
}

// TestResolveAfterInit_ToolboxAndManualVarsCoexist locks the bug both
// reviewers caught: when MissingToolboxEndpoints AND MissingManualVars
// are populated, the previously-exclusive switch hid the manual
// `azd env set` lines while still emitting `azd ai agent run` — so
// the user was directed to run locally with required manual vars
// still unset.
//
// Expected output for state with one toolbox + one unrelated manual
// var (e.g. an API key):
//
//  1. `azd provision`              — create the toolbox in Foundry
//  2. `azd ai agent doctor`        — optional existence-check follow-up
//  3. `azd env set MY_API_KEY …`   — surface the manual var
//  4. `azd ai agent run`           — start locally once the steps above are complete
//  5. `azd ai agent invoke --local` — secondary
//  6. `azd deploy`                 — trailing
//
// The `azd env set TOOLBOX_…` line must still NOT appear — that's the
// original (separate) bug Commit A locked out.
func TestResolveAfterInit_ToolboxAndManualVarsCoexist(t *testing.T) {
	t.Parallel()

	state := &State{
		HasProjectEndpoint: true,
		MissingToolboxEndpoints: []ResourceRef{
			{Name: "web-search-tools", ServiceName: "agent"},
		},
		MissingManualVars: []string{"MY_API_KEY"},
	}

	var buf strings.Builder
	require.NoError(t, PrintAllNext(&buf, ResolveAfterInit(state, nil)))
	rendered := buf.String()

	assert.Contains(t, rendered, "azd provision",
		"coexistence: toolbox sub-branch must still emit provision")
	assert.Contains(t, rendered, "azd ai agent doctor",
		"coexistence: toolbox sub-branch must still emit doctor follow-up")
	assert.Contains(t, rendered, "azd env set MY_API_KEY <value>",
		"coexistence: manual sub-branch must surface the unrelated env-set line")
	assert.Contains(t, rendered, "azd ai agent run",
		"coexistence: run follow-up must be emitted (no placeholders blocking)")
	assert.Contains(t, rendered, "once the steps above are complete",
		"coexistence: run description must reflect that both provision and env-set are prerequisites")
	assert.Contains(t, rendered, "azd ai agent invoke --local",
		"coexistence: invoke-local secondary must be emitted")
	assert.NotContains(t, rendered,
		"azd env set TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT",
		"coexistence: toolbox endpoint var must not be routed through `azd env set`")
	assert.Contains(t, rendered, "azd deploy", "trailing deploy reminder missing")
}

// TestResolveAfterInit_ToolboxAndManualVarsCoexistWithPlaceholders
// verifies the run/invoke suppression also fires for the coexistence
// case: when placeholders are also unresolved, the run + invoke-local
// pair must be suppressed regardless of which sub-branches contributed
// guidance above them.
func TestResolveAfterInit_ToolboxAndManualVarsCoexistWithPlaceholders(t *testing.T) {
	t.Parallel()

	state := &State{
		HasProjectEndpoint:     true,
		UnresolvedPlaceholders: []string{"AGENT_NAME"},
		MissingToolboxEndpoints: []ResourceRef{
			{Name: "web-search-tools", ServiceName: "agent"},
		},
		MissingManualVars: []string{"MY_API_KEY"},
	}

	var buf strings.Builder
	require.NoError(t, PrintAllNext(&buf, ResolveAfterInit(state, nil)))
	rendered := buf.String()

	assert.Contains(t, rendered,
		"edit azure.yaml: replace {{AGENT_NAME}} with the actual value",
		"placeholder fix-up missing")
	assert.Contains(t, rendered, "azd provision",
		"coexistence+placeholders: toolbox sub-branch must still emit provision")
	assert.Contains(t, rendered, "azd ai agent doctor",
		"coexistence+placeholders: toolbox sub-branch must still emit doctor follow-up")
	assert.Contains(t, rendered, "azd env set MY_API_KEY <value>",
		"coexistence+placeholders: manual sub-branch must still surface env-set")
	assert.NotContains(t, rendered, "azd ai agent run",
		"coexistence+placeholders: run must be suppressed while placeholders are unresolved")
	assert.NotContains(t, rendered, "azd ai agent invoke --local",
		"coexistence+placeholders: invoke-local must be suppressed while placeholders are unresolved")
	assert.Contains(t, rendered, "azd deploy", "trailing deploy reminder missing")
}

// TestRunFollowUpDescription exercises the helper directly so future
// changes to the description text (or a regression in the conditional
// branching) get caught even when the higher-level resolver tests
// happen to overlap on substrings.
func TestRunFollowUpDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		hasToolboxEndpoint bool
		hasManualVars      bool
		want               string
	}{
		{
			name:               "both",
			hasToolboxEndpoint: true,
			hasManualVars:      true,
			want:               "start the agent locally once the steps above are complete",
		},
		{
			name:               "toolbox only",
			hasToolboxEndpoint: true,
			want:               "start the agent locally once provision completes",
		},
		{
			name:          "manual only",
			hasManualVars: true,
			want:          "start the agent locally once the values above are set",
		},
		{
			// Defensive fallthrough — unreachable from ResolveAfterInit's
			// combined case (guarded by `hasToolboxEndpoints ||
			// hasManualVars`) but the helper is total over its inputs.
			name: "neither",
			want: "start the agent locally",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := runFollowUpDescription(tc.hasToolboxEndpoint, tc.hasManualVars)
			assert.Equal(t, tc.want, got)
		})
	}
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
			out := ResolveAfterInit(tt.state, nil)
			require.NotEmpty(t, out)

			// Walk the output:
			//   1. leading run of placeholder fix-ups (one per wantPlaceholders[i])
			//   2. optional middle command (provision / env set)
			//   3. optional `azd ai agent run`
			//   4. trailing `azd deploy`
			for i, name := range tt.wantPlaceholders {
				require.Less(t, i, len(out))
				assert.Equal(t,
					"edit azure.yaml: replace {{"+name+"}} with the actual value",
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
			name: "invocations protocol, no spec, no README → placeholder + tip",
			state: &State{
				Services: []ServiceState{{Name: "echo", Protocol: ProtocolInvocations}},
			},
			serviceName: "echo",
			want: []string{
				`azd ai agent invoke --local '<payload>'`,
				`curl http://localhost:<port>/invocations/docs/openapi.json`,
			},
		},
		{
			name: "responses protocol, no spec, no README → placeholder + tip",
			state: &State{
				Services: []ServiceState{{Name: "echo", Protocol: ProtocolResponses}},
			},
			serviceName: "echo",
			want: []string{
				`azd ai agent invoke --local '<payload>'`,
				`curl http://localhost:<port>/invocations/docs/openapi.json`,
			},
		},
		{
			name: "unknown protocol, no README → placeholder + tip",
			state: &State{
				Services: []ServiceState{{Name: "echo", Protocol: ""}},
			},
			serviceName: "echo",
			want: []string{
				`azd ai agent invoke --local '<payload>'`,
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
				`azd ai agent invoke --local '<payload>'`,
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
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := ResolveAfterRun(tt.state, tt.serviceName, nil)
			require.Len(t, out, len(tt.want))
			for i, snippet := range tt.want {
				assert.Contains(t, out[i].Command, snippet)
			}
		})
	}
}

func TestResolveAfterRun_NilState(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ResolveAfterRun(nil, "", nil))
}

func TestResolveAfterInvoke_Success(t *testing.T) {
	t.Parallel()

	t.Run("local success → deploy + monitor", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterInvoke(&State{}, InvokeLocal, "", nil)
		// Issue #7975 lines 168-181: local-invoke success surfaces
		// both `azd deploy` (ship to Azure) and the live-log monitor
		// follow-up (verify the deployed copy is healthy).
		require.Len(t, out, 2)

		assert.Equal(t, "azd deploy", out[0].Command)
		assert.Equal(t, "deploy the agent to Azure", out[0].Description)
		assert.False(t, out[0].Trailing,
			"primary suggestion must not be Trailing")

		assert.Equal(t, "azd ai agent monitor --follow", out[1].Command)
		assert.Equal(t, "view logs after deploying", out[1].Description)
		assert.False(t, out[1].Trailing,
			"secondary suggestion must not be Trailing")

		// Priority ordering matters: PrintNext / PrintAllNext stable-sort
		// by Priority ascending, so the slice position alone does NOT
		// guarantee the rendered order. Locking priorities here prevents
		// a future edit from accidentally inverting the values and
		// making `monitor --follow` render before `azd deploy`. Mirrors
		// the failure-path pattern on the remote-failure test below.
		assert.Less(t, out[0].Priority, out[1].Priority,
			"deploy must sort before monitor --follow")
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
		name        string
		status      AgentVersionStatus
		serviceName string
		wantCmdHas  string
	}{
		{"Creating → monitor system", AgentVersionCreating, "echo", "azd ai agent monitor --type system --follow"},
		{"Failed → monitor --follow", AgentVersionFailed, "echo", "azd ai agent monitor --follow"},
		{"Deleting → redeploy", AgentVersionDeleting, "echo", "azd deploy"},
		{"Deleted → redeploy", AgentVersionDeleted, "echo", "azd deploy"},
		{"empty status → monitor --follow", "", "echo", "azd ai agent monitor --follow"},
		{"unknown status → re-check show", "Transitioning", "echo", "azd ai agent show echo"},
		{"unknown status without agent name → bare show", "Transitioning", "", "azd ai agent show"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Same-name case: service and agent names align (common when deploy
			// doesn't append a suffix). Divergent-name behavior is exercised by
			// TestResolveAfterShow_DivergentNames below — the resolver always
			// emits the service name in the unknown-status re-check.
			out := ResolveAfterShow(&State{AgentStatus: string(tt.status)}, tt.serviceName)
			require.NotEmpty(t, out)
			assert.Contains(t, out[0].Command, tt.wantCmdHas)
		})
	}
}

func TestResolveAfterShow_ActiveAndIdleReturnNil(t *testing.T) {
	t.Parallel()

	for _, status := range []AgentVersionStatus{AgentVersionActive, AgentVersionIdle} {
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			state := &State{
				AgentStatus: string(status),
				Services:    []ServiceState{{Name: "echo", Protocol: ProtocolInvocations}},
			}
			assert.Nil(t, ResolveAfterShow(state, "echo"))
		})
	}
}

// TestResolveAfterShow_DivergentNames locks the divergent-name contract:
// when the azure.yaml service name and the deployed Foundry agent name
// differ, the unknown-status re-check suggestion uses the SERVICE name
// as the positional because show.go's lookup matches by service name.
func TestResolveAfterShow_DivergentNames(t *testing.T) {
	t.Parallel()

	t.Run("unknown status: re-check uses service name", func(t *testing.T) {
		t.Parallel()
		out := ResolveAfterShow(&State{AgentStatus: "Transitioning"}, "svc-echo")
		require.Len(t, out, 1)
		assert.Equal(t, "azd ai agent show svc-echo", out[0].Command)
	})
}

func TestResolveAfterShow_NilState(t *testing.T) {
	t.Parallel()
	assert.Nil(t, ResolveAfterShow(nil, "echo"))
}

func TestResolveAfterDeploy(t *testing.T) {
	t.Parallel()

	t.Run("single agent, cached payload available → 2 qualified lines, no README hint", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{{Name: "echo", RelativePath: "./src/echo"}}}
		cached := func(_ string) string { return `{"q":"x"}` }
		out := ResolveAfterDeploy(state, cached, nil)
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent show echo", out[0].Command)
		assert.Equal(t, "verify it's running", out[0].Description)
		assert.Equal(t, `azd ai agent invoke echo '{"q":"x"}'`, out[1].Command)
		assert.Equal(t, "test the deployment", out[1].Description)
	})

	t.Run("single agent, no cached payload, README on disk → README then placeholder invoke", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{{Name: "echo", RelativePath: "./src/echo", Protocol: ProtocolResponses}}}
		readme := func(p string) bool { return p == "./src/echo" }
		out := ResolveAfterDeploy(state, nil, readme)
		require.Len(t, out, 3)
		assert.Equal(t, "azd ai agent show echo", out[0].Command)
		assert.Equal(t, "verify it's running", out[0].Description)
		assert.Equal(t, "see src/echo/README.md", out[1].Command)
		assert.Equal(t, "find the sample-specific payload", out[1].Description)
		assert.Equal(t, `azd ai agent invoke echo '<payload>'`, out[2].Command)
		assert.Equal(t, "test with the sample-specific payload", out[2].Description)
	})

	t.Run("single root agent, README on disk → root README hint", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			rel  string
		}{
			{name: "empty", rel: ""},
			{name: "dot", rel: "."},
			{name: "dot slash", rel: "./"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				state := &State{Services: []ServiceState{{Name: "echo", RelativePath: tt.rel, Protocol: ProtocolResponses}}}
				readme := func(p string) bool { return p == tt.rel }

				out := ResolveAfterDeploy(state, nil, readme)

				require.Len(t, out, 3)
				assert.Equal(t, "see README.md", out[1].Command)
				assert.Equal(t, "find the sample-specific payload", out[1].Description)
				assert.Equal(t, `azd ai agent invoke echo '<payload>'`, out[2].Command)
			})
		}
	})

	t.Run("multi-agent → all shows first, then all invokes, with per-agent descriptions", func(t *testing.T) {
		// Spec source: issue #7975 lines 238-241 — multi-agent layout
		// groups shows before invokes (not interleaved) and bakes the
		// agent name into the description so users can scan vertically.
		t.Parallel()
		state := &State{Services: []ServiceState{
			{Name: "alpha", Protocol: ProtocolInvocations},
			{Name: "beta", Protocol: ProtocolResponses},
		}}
		out := ResolveAfterDeploy(state, nil, nil)
		require.Len(t, out, 4)
		assert.Equal(t, "azd ai agent show alpha", out[0].Command)
		assert.Equal(t, "verify alpha is running", out[0].Description)
		assert.Equal(t, "azd ai agent show beta", out[1].Command)
		assert.Equal(t, "verify beta is running", out[1].Description)
		assert.Equal(t, `azd ai agent invoke alpha '<payload>'`, out[2].Command)
		assert.Equal(t, "test alpha", out[2].Description)
		assert.Equal(t, `azd ai agent invoke beta '<payload>'`, out[3].Command)
		assert.Equal(t, "test beta", out[3].Description)
	})

	t.Run("multi-agent README hint placement → before the corresponding placeholder invoke", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{
			{Name: "alpha", RelativePath: "./src/alpha", Protocol: ProtocolResponses},
			{Name: "beta", Protocol: ProtocolResponses},
		}}
		readme := func(p string) bool { return p == "./src/alpha" }
		out := ResolveAfterDeploy(state, nil, readme)
		// 2 shows + 2 invokes + 1 README hint for alpha = 5 entries.
		require.Len(t, out, 5)
		assert.Equal(t, "azd ai agent show alpha", out[0].Command)
		assert.Equal(t, "azd ai agent show beta", out[1].Command)
		assert.Equal(t, "see src/alpha/README.md", out[2].Command)
		assert.Equal(t, "find the sample-specific payload", out[2].Description)
		assert.Equal(t, `azd ai agent invoke alpha '<payload>'`, out[3].Command)
		assert.Equal(t, "test alpha with the sample-specific payload", out[3].Description)
		assert.Equal(t, `azd ai agent invoke beta '<payload>'`, out[4].Command)
		assert.Equal(t, "test beta", out[4].Description)
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

	t.Run("cached payload containing apostrophe → POSIX-escaped on qualified invoke", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{{Name: "echo", RelativePath: "./src/echo"}}}
		cached := func(_ string) string { return `{"q":"don't"}` }
		out := ResolveAfterDeploy(state, cached, nil)
		require.Len(t, out, 2)
		assert.Equal(t, `azd ai agent invoke echo '{"q":"don'\''t"}'`, out[1].Command)
	})

	t.Run("ForceQualified=true on len==1 → no-op, output identical to default", func(t *testing.T) {
		// Backward-compat assertion: B9 makes all output qualified by
		// default; ForceQualified is preserved as a no-op for callers
		// (e.g., doctor) that still pass it. Result must match the
		// "no opts" call exactly.
		t.Parallel()
		state := &State{Services: []ServiceState{
			{Name: "echo", RelativePath: "./src/echo", Protocol: ProtocolInvocations},
		}}
		out := ResolveAfterDeploy(state, nil, nil, AfterDeployOpts{ForceQualified: true})
		baseline := ResolveAfterDeploy(state, nil, nil)
		require.Equal(t, baseline, out)
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent show echo", out[0].Command)
		assert.Equal(t, `azd ai agent invoke echo '<payload>'`, out[1].Command)
	})

	t.Run("ForceQualified=false on len==1 → no-op, also qualified", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{
			{Name: "echo", RelativePath: "./src/echo", Protocol: ProtocolInvocations},
		}}
		out := ResolveAfterDeploy(state, nil, nil, AfterDeployOpts{ForceQualified: false})
		require.Len(t, out, 2)
		assert.Equal(t, "azd ai agent show echo", out[0].Command)
		assert.Equal(t, `azd ai agent invoke echo '<payload>'`, out[1].Command)
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

	t.Run("ForceQualified=true on multi-agent → identical to default multi-agent layout", func(t *testing.T) {
		t.Parallel()
		state := &State{Services: []ServiceState{
			{Name: "alpha", Protocol: ProtocolInvocations},
			{Name: "beta", Protocol: ProtocolResponses},
		}}
		out := ResolveAfterDeploy(state, nil, nil, AfterDeployOpts{ForceQualified: true})
		require.Len(t, out, 4)
		assert.Equal(t, "azd ai agent show alpha", out[0].Command)
		assert.Equal(t, "azd ai agent show beta", out[1].Command)
		assert.Equal(t, `azd ai agent invoke alpha '<payload>'`, out[2].Command)
		assert.Equal(t, `azd ai agent invoke beta '<payload>'`, out[3].Command)
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
