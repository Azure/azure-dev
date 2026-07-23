// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// ---- Check `local.manual-env-vars` ----

// fakeAssembler returns a closure suitable for Dependencies.assembleState.
// Each variant locks one branch of the production check (nil-state,
// nil-state with errs, populated MissingManualVars, etc.) without
// standing up an azd project on disk.
func fakeAssembler(
	state *nextstep.State, errs ...error,
) func(context.Context, *azdext.AzdClient) (*nextstep.State, []error) {
	return func(_ context.Context, _ *azdext.AzdClient) (*nextstep.State, []error) {
		if len(errs) == 0 {
			return state, nil
		}
		return state, errs
	}
}

func TestCheckManualEnvVars_NoClient_Skips(t *testing.T) {
	t.Parallel()

	check := newCheckManualEnvVars(Dependencies{AzdClient: nil})
	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "azd extension not reachable")
}

func TestCheckManualEnvVars_PriorAgentYAMLFailed_Skips(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckManualEnvVars(Dependencies{
		AzdClient: client,
		// Defensive: if the skip-guard fails, the production path
		// would call nextstep.AssembleState against the real (empty)
		// client and produce a different Status. The fake assembler
		// here asserts the cascade short-circuits before the
		// assembler is reached.
		assembleState: func(context.Context, *azdext.AzdClient) (*nextstep.State, []error) {
			t.Fatalf("assembler should not be called when local.agent-yaml-valid failed")
			return nil, nil
		},
	})

	prior := []Result{{ID: "local.agent-yaml-valid", Status: StatusFail}}
	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "agent definition check failed")
}

func TestCheckManualEnvVars_PriorAgentYAMLSkipped_AlsoSkips(t *testing.T) {
	// Covers the cascade: a deeper upstream (e.g. azure-yaml) failed,
	// agent-yaml-valid was therefore skipped, and this check must
	// inherit the skip rather than running on a half-loaded project.
	// Without this propagation users would see a misleading
	// "no manual env vars are missing" Pass underneath the real bug.
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckManualEnvVars(Dependencies{
		AzdClient: client,
		assembleState: func(context.Context, *azdext.AzdClient) (*nextstep.State, []error) {
			t.Fatalf("assembler should not be called when upstream was skipped")
			return nil, nil
		},
	})

	prior := []Result{{ID: "local.agent-yaml-valid", Status: StatusSkip}}
	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
}

func TestCheckManualEnvVars_PriorEnvironmentSelectedFailed_Skips(t *testing.T) {
	// Regression test for issue #7975 false-Pass scenario raised in
	// C9 review: when no azd environment is selected,
	// nextstep.AssembleState's detectMissingVars block is gated on
	// `envName != ""` (state.go) and silently returns an empty
	// MissingManualVars. Without an explicit guard against the
	// `local.environment-selected` failure, this check would
	// produce a Pass ("no manual env vars are missing") in a state
	// where it never actually examined any env values.
	//
	// The fake assembler t.Fatalf's to assert the check
	// short-circuits at the guard rather than calling into
	// AssembleState — the cascade must be observable in the test,
	// not just emergent from the assembler's behavior.
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckManualEnvVars(Dependencies{
		AzdClient: client,
		assembleState: func(context.Context, *azdext.AzdClient) (*nextstep.State, []error) {
			t.Fatalf("assembler should not be called when local.environment-selected failed")
			return nil, nil
		},
	})

	prior := []Result{
		{ID: "local.azure-yaml", Status: StatusPass},
		{ID: "local.environment-selected", Status: StatusFail},
		{ID: "local.agent-service-detected", Status: StatusPass},
		{ID: "local.agent-yaml-valid", Status: StatusPass},
	}
	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "no azd environment selected")
}

func TestCheckManualEnvVars_PriorEnvironmentSelectedSkipped_AlsoSkips(t *testing.T) {
	// Symmetric to the failed-environment-selected case: if the env
	// check itself was skipped (e.g. azure-yaml failed deeper
	// upstream), the manual-env check must also skip rather than
	// flake out a confident Pass.
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckManualEnvVars(Dependencies{
		AzdClient: client,
		assembleState: func(context.Context, *azdext.AzdClient) (*nextstep.State, []error) {
			t.Fatalf("assembler should not be called when env-selected was skipped")
			return nil, nil
		},
	})

	prior := []Result{
		{ID: "local.environment-selected", Status: StatusSkip},
		{ID: "local.agent-yaml-valid", Status: StatusPass},
	}
	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
}

func TestCheckManualEnvVars_AllVarsSet_Passes(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckManualEnvVars(Dependencies{
		AzdClient:     client,
		assembleState: fakeAssembler(&nextstep.State{HasProjectEndpoint: true}),
	})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, "no manual env vars are missing")
	require.Empty(t, got.Suggestion)
	require.Nil(t, got.Details)
}

func TestCheckManualEnvVars_OneMissing_Fails(t *testing.T) {
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckManualEnvVars(Dependencies{
		AzdClient: client,
		assembleState: fakeAssembler(&nextstep.State{
			HasProjectEndpoint: true,
			MissingManualVars:  []string{"MY_API_KEY"},
		}),
	})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "1 manual env var(s)")
	require.Contains(t, got.Message, "MY_API_KEY")
	// Single-var case: bare command, no "repeat" clause — adding it
	// would imply the user missed something they didn't.
	require.Equal(t, "Run `azd env set MY_API_KEY <value>`.", got.Suggestion)
	require.NotContains(t, got.Suggestion, "Repeat")
	require.NotContains(t, got.Suggestion, "likewise")
	require.Equal(t, []string{"MY_API_KEY"}, got.Details["missingManualVars"])
}

func TestCheckManualEnvVars_MultipleMissing_FailsWithSortedList(t *testing.T) {
	// Sort order is part of the contract — the rendered Message must
	// be deterministic across runs, and the renderer-paired suggestion
	// in the nextstep manual-vars branch sorts identically.
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckManualEnvVars(Dependencies{
		AzdClient: client,
		assembleState: fakeAssembler(&nextstep.State{
			HasProjectEndpoint: true,
			MissingManualVars:  []string{"DELTA", "ALPHA", "ECHO", "BRAVO"},
		}),
	})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "4 manual env var(s)")
	require.Contains(t, got.Message, "ALPHA, BRAVO, DELTA, ECHO")
	// Suggestion uses the first alphabetically sorted var as the
	// paste-ready example. Confirms the suggestion text is keyed off
	// the SORTED list (not the input order), so the same project
	// always yields the same example regardless of map iteration
	// order in the upstream classifier.
	require.Equal(t,
		"Run `azd env set ALPHA <value>`. Repeat for each of the other variables listed above.",
		got.Suggestion)
	require.Equal(t,
		[]string{"ALPHA", "BRAVO", "DELTA", "ECHO"},
		got.Details["missingManualVars"])
}

func TestCheckManualEnvVars_NilStateFromAssembler_Fails(t *testing.T) {
	// Defensive contract test: nextstep.AssembleState today always
	// returns a non-nil State. Pin the doctor's defensive branch so
	// a future contract drift can't degrade the user-facing report
	// into a panic-dereference.
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckManualEnvVars(Dependencies{
		AzdClient:     client,
		assembleState: fakeAssembler(nil, errors.New("boom: bicep parse failed")),
	})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "failed to assemble agent state")
	require.Contains(t, got.Message, "boom: bicep parse failed")
	require.Contains(t, got.Suggestion, "state assembly returned nil")
}

func TestCheckManualEnvVars_NilStateNoErrors_FailsWithFallback(t *testing.T) {
	// Edge case: assembler returns (nil, nil). Today unreachable in
	// production but the doctor must still produce a non-panicking
	// Fail with a sensible message rather than dereferencing nil.
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckManualEnvVars(Dependencies{
		AzdClient:     client,
		assembleState: fakeAssembler(nil),
	})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "failed to assemble agent state")
	require.Contains(t, got.Message, "unknown error")
}

func TestCheckManualEnvVars_NonFatalErrorsButStateOK_Passes(t *testing.T) {
	// nextstep.AssembleState surfaces best-effort errors via the errs
	// slice while still returning a usable State. The doctor must
	// trust the populated State (PASS when MissingManualVars is empty)
	// and not be tripped up by ancillary errs like a missing
	// AI_AGENT_PENDING_PROVISION key.
	t.Parallel()

	client := newTestAzdClient(t, &fakeProjectServer{}, &fakeEnvironmentServer{})
	check := newCheckManualEnvVars(Dependencies{
		AzdClient: client,
		assembleState: fakeAssembler(
			&nextstep.State{HasProjectEndpoint: true},
			errors.New("read AI_AGENT_PENDING_PROVISION: key not found"),
		),
	})

	got := check.Fn(t.Context(), Options{}, nil)

	require.Equal(t, StatusPass, got.Status)
}

func TestNewLocalChecks_IncludesManualEnvVarsLast(t *testing.T) {
	// Pin C9's insertion point: the manual-env-vars check must follow
	// agent-yaml-valid so its skip-cascade against the upstream chain
	// is exercised by the runner's prior-results slice. Locks the
	// ordering invariant that the design's "checks 1-7" table relies
	// on for failure-cascade coherence.
	t.Parallel()

	checks := NewLocalChecks(Dependencies{})
	require.NotEmpty(t, checks)

	ids := make([]string, len(checks))
	for i, c := range checks {
		ids[i] = c.ID
	}
	require.Contains(t, ids, "local.manual-env-vars")

	var yamlIdx, manualIdx int = -1, -1
	for i, id := range ids {
		switch id {
		case "local.agent-yaml-valid":
			yamlIdx = i
		case "local.manual-env-vars":
			manualIdx = i
		}
	}
	require.NotEqual(t, -1, yamlIdx, "agent-yaml-valid must be in NewLocalChecks")
	require.NotEqual(t, -1, manualIdx, "manual-env-vars must be in NewLocalChecks")
	require.Greater(t, manualIdx, yamlIdx,
		"manual-env-vars must come after agent-yaml-valid for the skip-cascade")
}
