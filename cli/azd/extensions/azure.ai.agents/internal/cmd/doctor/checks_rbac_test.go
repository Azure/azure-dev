// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// rbacProbeStub builds a Dependencies whose probeDeveloperRBAC seam
// returns a fixed (result, err) pair. AzdClient is set to a non-nil
// placeholder so the early `deps.AzdClient == nil` Skip does not
// short-circuit the check. The placeholder is never dereferenced
// inside the check body (the seam intercepts before any client call
// happens), so a zero-value AzdClient is safe.
func rbacProbeStub(result *project.DeveloperRBACResult, err error) Dependencies {
	return Dependencies{
		AzdClient: &azdext.AzdClient{},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			return result, err
		},
	}
}

// passingPriorsForRBAC returns the upstream prior results the RBAC
// check requires to actually run: environment-selected + auth Pass.
// Notably it does NOT include a remote.foundry-endpoint result
// because the design's dependency matrix (line 115) explicitly
// excludes that cascade.
func passingPriorsForRBAC() []Result {
	return []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "remote.auth", Status: StatusPass},
	}
}

// ---- Skip-cascade contract ----

func TestCheckRBAC_SkipsWhenAzdClientMissing(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			t.Fatal("probe must not be invoked when AzdClient is nil")
			return nil, nil
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "azd extension not reachable")
}

func TestCheckRBAC_SkipsWhenEnvNotSelected(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(rbacProbeStub(nil, errors.New("probe should not have been called")))
	prior := []Result{
		{ID: "local.environment-selected", Status: StatusFail},
		{ID: "remote.auth", Status: StatusPass},
	}

	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "azd environment")
	require.Contains(t, got.Message, "local.environment-selected")
}

func TestCheckRBAC_SkipsWhenAuthFailed(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(rbacProbeStub(nil, errors.New("probe should not have been called")))
	prior := []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "remote.auth", Status: StatusFail},
	}

	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "auth probe")
	require.Contains(t, got.Message, "remote.auth")
}

// Per the design's dependency matrix (line 115), RBAC reads ARM and
// is NOT dependent on the Foundry data-plane reachability check. A
// failing `remote.foundry-endpoint` (e.g., a transient DNS hiccup)
// should NOT prevent the user from learning that their role
// assignment is missing.
func TestCheckRBAC_DoesNotSkipOnFoundryEndpointFail(t *testing.T) {
	t.Parallel()

	probeCalled := false
	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, projectID string) (*project.DeveloperRBACResult, error) {
			probeCalled = true
			require.Equal(t,
				"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj",
				projectID)
			return &project.DeveloperRBACResult{
				PrincipalID:         "principal-oid",
				PrincipalDisplay:    "Alice",
				HasSufficientAIRole: true,
				ProjectScope:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj",
				AccountName:         "acct",
				ProjectName:         "proj",
			}, nil
		},
	})
	// Mark `remote.foundry-endpoint` as Fail in priors — the RBAC
	// check should still run because its skip-cascade explicitly
	// excludes the data-plane check.
	prior := []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{ID: "remote.auth", Status: StatusPass},
		{ID: "remote.foundry-endpoint", Status: StatusFail},
	}

	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusPass, got.Status,
		"RBAC check must Pass even when foundry-endpoint check Failed")
	require.True(t, probeCalled,
		"probe must have been invoked despite the foundry-endpoint Fail in priors")
}

// ---- classifyRBACResult: Pass / Fail mapping (the heart of the check) ----

func TestClassifyRBACResult_PassesWhenRoleHeld(t *testing.T) {
	t.Parallel()

	got := classifyRBACResult(&project.DeveloperRBACResult{
		PrincipalID:         "principal-oid",
		PrincipalDisplay:    "Alice Example",
		HasSufficientAIRole: true,
		ProjectScope:        "/subscriptions/sub/rg/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj",
		AccountName:         "acct",
		ProjectName:         "proj",
	}, false /* redacted */)

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, redactedDisplayLabel,
		"default redacted mode must use the generic display label, not the raw PrincipalDisplay")
	require.NotContains(t, got.Message, "Alice Example",
		"raw PrincipalDisplay must not leak in default redacted mode (UPN safety)")
	require.Contains(t, got.Message, "acct/proj")
	require.Empty(t, got.Suggestion,
		"Pass results should not carry a Suggestion")
	require.Empty(t, got.Links,
		"Pass results should not carry Links")
	// Details should be present and indicate the result, but principal
	// id and scope are redacted in the default mode.
	require.Equal(t, true, got.Details["hasSufficientAIRole"])
	require.Equal(t, "acct", got.Details["accountName"])
	require.Equal(t, "proj", got.Details["projectName"])
	require.Equal(t, redactedPlaceholder, got.Details["principalId"])
	require.Equal(t, redactedPlaceholder, got.Details["projectScope"])
	require.Equal(t, redactedPlaceholder, got.Details["principalDisplay"])
}

func TestClassifyRBACResult_PassesAndPreservesIdentitiesWhenUnredacted(t *testing.T) {
	t.Parallel()

	scope := "/subscriptions/sub/rg/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	got := classifyRBACResult(&project.DeveloperRBACResult{
		PrincipalID:         "principal-oid",
		PrincipalDisplay:    "Alice Example",
		HasSufficientAIRole: true,
		ProjectScope:        scope,
		AccountName:         "acct",
		ProjectName:         "proj",
	}, true /* unredacted */)

	require.Equal(t, StatusPass, got.Status)
	require.Equal(t, "principal-oid", got.Details["principalId"])
	require.Equal(t, scope, got.Details["projectScope"])
	require.Equal(t, "Alice Example", got.Details["principalDisplay"])
}

func TestClassifyRBACResult_FailsWithTemplatedAzCommand(t *testing.T) {
	t.Parallel()

	scope := "/subscriptions/sub/rg/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	got := classifyRBACResult(&project.DeveloperRBACResult{
		PrincipalID:         "principal-oid",
		PrincipalDisplay:    "Alice Example",
		HasSufficientAIRole: false,
		ProjectScope:        scope,
		AccountName:         "acct",
		ProjectName:         "proj",
	}, false /* redacted */)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, redactedDisplayLabel,
		"default redacted mode must use the generic display label in the Message")
	require.NotContains(t, got.Message, "Alice Example",
		"raw PrincipalDisplay must not leak in default redacted Fail Message")
	require.Contains(t, got.Message, "acct/proj")
	require.Contains(t, got.Message, "does not have the required role")

	// The suggestion must contain the templated az command, with
	// shell-safe ALL_CAPS placeholders in the default redacted mode
	// (NOT `<redacted>`, because bash/zsh treat `<word>` as input
	// redirection — a literal copy-paste must fail safely or work).
	require.Contains(t, got.Suggestion, "az role assignment create")
	require.Contains(t, got.Suggestion, "Azure AI User")
	require.Contains(t, got.Suggestion, shellSafePlaceholderID,
		"redacted mode must substitute the shell-safe placeholder for the principal id")
	require.Contains(t, got.Suggestion, shellSafePlaceholderScope,
		"redacted mode must substitute the shell-safe placeholder for the scope")
	require.NotContains(t, got.Suggestion, redactedPlaceholder,
		"the `<redacted>` token must NOT appear in the templated az command "+
			"(it triggers shell redirection on copy-paste)")
	require.NotContains(t, got.Suggestion, "principal-oid",
		"raw principal id must not leak in redacted suggestion")
	require.NotContains(t, got.Suggestion, "/subscriptions/sub",
		"raw scope must not leak in redacted suggestion")
	require.NotEmpty(t, got.Links,
		"Fail must carry a learn.microsoft.com link for the role-assignment guide")
}

func TestClassifyRBACResult_FailsAndIncludesIdentitiesWhenUnredacted(t *testing.T) {
	t.Parallel()

	scope := "/subscriptions/sub/rg/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"
	got := classifyRBACResult(&project.DeveloperRBACResult{
		PrincipalID:         "principal-oid",
		PrincipalDisplay:    "Alice Example",
		HasSufficientAIRole: false,
		ProjectScope:        scope,
		AccountName:         "acct",
		ProjectName:         "proj",
	}, true /* unredacted */)

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "Alice Example",
		"unredacted mode must include the real display name in the Message")
	require.Contains(t, got.Suggestion, "principal-oid",
		"unredacted mode must include the real principal id")
	require.Contains(t, got.Suggestion, "/subscriptions/sub",
		"unredacted mode must include the real scope")
	require.NotContains(t, got.Suggestion, shellSafePlaceholderID,
		"unredacted mode must not substitute placeholder for the principal id")
	require.NotContains(t, got.Suggestion, shellSafePlaceholderScope,
		"unredacted mode must not substitute placeholder for the scope")
}

func TestClassifyRBACResult_FallsBackToGenericDisplayWhenMissing(t *testing.T) {
	t.Parallel()

	// Both with-display-redacted and missing-display paths should
	// converge on the same redactedDisplayLabel for a uniform UX.
	got := classifyRBACResult(&project.DeveloperRBACResult{
		PrincipalID:         "principal-oid",
		PrincipalDisplay:    "", // Graph didn't return a display name
		HasSufficientAIRole: true,
		AccountName:         "acct",
		ProjectName:         "proj",
		ProjectScope:        "/x",
	}, true /* unredacted: empty-display fallback still applies */)

	require.Contains(t, got.Message, redactedDisplayLabel,
		"missing display name must fall back to the generic label "+
			"even when unredacted is true")
}

// ---- Probe-error branches ----

func TestCheckRBAC_SkipsOnCancellationDuringProbe(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			return nil, context.Canceled
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status,
		"cancellation must propagate as a clean Skip, not a Fail")
	require.Contains(t, got.Message, "cancelled")
}

func TestCheckRBAC_SkipsOnTransientProbeError(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			return nil, errors.New("dial tcp: i/o timeout\nsecond line should be stripped")
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status,
		"transient probe error must Skip (not Fail) to avoid false alarms")
	require.Contains(t, got.Message, "could not query role assignments")
	require.Contains(t, got.Message, "dial tcp: i/o timeout")
	require.NotContains(t, got.Message, "second line should be stripped",
		"firstLine helper must strip subsequent lines from the error")
	require.NotContains(t, got.Details, "probeError",
		"default redacted mode must omit the raw probe error from Details")
}

// TestCheckRBAC_SkipsWhenProjectIDMalformed exercises the upfront
// ValidateProjectResourceID gate added in response to Sonnet 4.6's
// review of commit 0c4d5ee31. Without this gate, a malformed
// AZURE_AI_PROJECT_ID (e.g., a URL instead of an ARM resource ID)
// would propagate through parseAgentIdentityInfo inside
// QueryDeveloperRBAC and surface as a generic "check your network"
// Suggestion — actively misleading the user toward network debugging
// for what is a pure configuration error.
func TestCheckRBAC_SkipsWhenProjectIDMalformed(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "https://example.com/not-an-arm-resource-id", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			t.Fatal("probe must not be invoked when projectID fails the upfront validation")
			return nil, nil
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "is not a valid Foundry project ARM resource ID")
	require.Contains(t, got.Suggestion, "azd env set AZURE_AI_PROJECT_ID")
	require.NotContains(t, got.Suggestion, "graph.microsoft.com",
		"malformed-ID Skip must NOT surface the network-retry Suggestion")
	require.NotContains(t, got.Details, "validateError",
		"default redacted mode must omit the raw validate error from Details")
}

// TestCheckRBAC_SkipsWhenProjectIDMalformed_UnredactedSurfacesError
// pins that --unredacted exposes the raw validation error so
// interactive users get the precise reason the resource ID failed
// the shape check.
func TestCheckRBAC_SkipsWhenProjectIDMalformed_UnredactedSurfacesError(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "https://example.com/not-an-arm-resource-id", nil
		},
	})

	got := check.Fn(t.Context(), Options{Unredacted: true}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status)
	require.NotNil(t, got.Details["validateError"],
		"unredacted mode must expose the raw validation error in Details")
}

// TestCheckRBAC_SkipsOnSPNToken pins the project.ErrSPNDelegatedAuthRequired
// branch added in response to GPT-5.5's review finding. Without this
// branch, users signed in via `azd auth login --client-id ...`
// (service-principal flow) get a confusing "check your network"
// Suggestion when the underlying Graph /me call rejects the app-only
// token. The targeted SPN Skip tells them to switch to user-delegated
// sign-in.
func TestCheckRBAC_SkipsOnSPNToken(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			return nil, fmt.Errorf("%w: graph response: %s",
				project.ErrSPNDelegatedAuthRequired,
				"/me request is only valid with delegated authentication flow")
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "user-delegated",
		"SPN Skip message must reference user-delegated sign-in")
	require.Contains(t, got.Suggestion, "azd auth login",
		"SPN Skip Suggestion must point at user-delegated sign-in")
}

// TestCheckRBAC_SkipsOnInvalidProjectIDSentinelFromProbe pins the
// defensive ErrInvalidProjectResourceID branch in
// classifyRBACProbeError. The upfront ValidateProjectResourceID
// gate catches this normally, but a future code path that bypasses
// the gate (or returns a wrapped sentinel from somewhere inside the
// probe stack) must still surface the configuration-error Suggestion.
func TestCheckRBAC_SkipsOnInvalidProjectIDSentinelFromProbe(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			// Return a VALID ID so the upfront validation passes
			// and the probe seam is reached. The probe then returns
			// a wrapped ErrInvalidProjectResourceID to exercise the
			// defensive branch.
			return "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			return nil, fmt.Errorf("%w: simulated inner parse failure",
				project.ErrInvalidProjectResourceID)
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "is not a valid Foundry project ARM resource ID")
	require.Contains(t, got.Suggestion, "azd env set",
		"defensive sentinel branch must surface the configuration Suggestion")
	require.NotContains(t, got.Suggestion, "graph.microsoft.com",
		"defensive sentinel branch must NOT surface the network-retry Suggestion")
}

// TestCheckRBAC_TransientProbeErrorScrubsScopeARNs pins the scope-
// redaction added in response to Opus xhigh + GPT-5.5's finding that
// azcore.ResponseError.Error() puts the full ARM URL (with
// subscription / resource group / account) on the first line, which
// would otherwise leak past the doctor's redaction model.
func TestCheckRBAC_TransientProbeErrorScrubsScopeARNs(t *testing.T) {
	t.Parallel()

	leakyErr := "GET https://management.azure.com/subscriptions/" +
		"11111111-1111-1111-1111-111111111111/resourceGroups/" +
		"super-secret-rg/providers/Microsoft.CognitiveServices/accounts/" +
		"super-secret-acct/providers/Microsoft.Authorization/roleAssignments " +
		"-> RESPONSE 500: transient ARM failure"

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/11111111-1111-1111-1111-111111111111/" +
				"resourceGroups/super-secret-rg/providers/" +
				"Microsoft.CognitiveServices/accounts/super-secret-acct/" +
				"projects/super-secret-proj", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			return nil, errors.New(leakyErr)
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status)
	require.NotContains(t, got.Message, "11111111-1111-1111-1111-111111111111",
		"subscription ID GUID must be redacted out of the probe error Message")
	require.NotContains(t, got.Message, "super-secret-rg",
		"resource group name must be redacted out of the probe error Message")
	require.NotContains(t, got.Message, "super-secret-acct",
		"account name must be redacted out of the probe error Message")
	require.NotContains(t, got.Message, "/subscriptions/",
		"raw `/subscriptions/...` path must be redacted out of the Message")
}

// TestCheckRBAC_TransientProbeErrorPreservesScopesWhenUnredacted
// pins that --unredacted does NOT scrub the probe error, so
// interactive users can still see the raw URL for debugging.
func TestCheckRBAC_TransientProbeErrorPreservesScopesWhenUnredacted(t *testing.T) {
	t.Parallel()

	leakyErr := "GET https://management.azure.com/subscriptions/" +
		"11111111-1111-1111-1111-111111111111/resourceGroups/rg " +
		"-> RESPONSE 500"

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/11111111-1111-1111-1111-111111111111/" +
				"resourceGroups/rg/providers/Microsoft.CognitiveServices/" +
				"accounts/acct/projects/proj", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			return nil, errors.New(leakyErr)
		},
	})

	got := check.Fn(t.Context(), Options{Unredacted: true}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "11111111-1111-1111-1111-111111111111",
		"unredacted mode must preserve the raw subscription ID in the Message")
	require.Equal(t, leakyErr, got.Details["probeError"],
		"unredacted mode must surface the raw probe error in Details")
}

func TestCheckRBAC_SkipsWhenProjectIDReaderErrors(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "", errors.New("rpc error: code = Unavailable desc = connection closed")
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			t.Fatal("probe must not be invoked when readProjectResourceID errors")
			return nil, nil
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "could not read AZURE_AI_PROJECT_ID")
	require.Contains(t, got.Suggestion, "azd provision")
	require.Contains(t, got.Suggestion, "azd env set AZURE_AI_PROJECT_ID")
}

func TestCheckRBAC_SkipsWhenProjectIDEmpty(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			t.Fatal("probe must not be invoked when AZURE_AI_PROJECT_ID is unset")
			return nil, nil
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "AZURE_AI_PROJECT_ID is not set")
	require.Contains(t, got.Suggestion, "azd provision")
}

// ---- End-to-end probe-injection: Pass / Fail wired through the check ----

func TestCheckRBAC_PassesWhenProbeReturnsRole(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			return &project.DeveloperRBACResult{
				PrincipalID:         "principal-oid",
				PrincipalDisplay:    "Alice Example",
				HasSufficientAIRole: true,
				ProjectScope:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj",
				AccountName:         "acct",
				ProjectName:         "proj",
			}, nil
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, redactedDisplayLabel,
		"default redacted mode must use the generic display label, not the raw display name")
	require.NotContains(t, got.Message, "Alice Example",
		"raw PrincipalDisplay must not leak through the check Fn in default mode")
	require.Contains(t, got.Message, "acct/proj")
}

func TestCheckRBAC_FailsWhenProbeReturnsNoRole(t *testing.T) {
	t.Parallel()

	check := newCheckRBAC(Dependencies{
		AzdClient: &azdext.AzdClient{},
		readProjectResourceIDFn: func(_ context.Context, _ *azdext.AzdClient) (string, error) {
			return "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj", nil
		},
		probeDeveloperRBAC: func(_ context.Context, _ *azdext.AzdClient, _ string) (*project.DeveloperRBACResult, error) {
			return &project.DeveloperRBACResult{
				PrincipalID:         "principal-oid",
				PrincipalDisplay:    "Alice Example",
				HasSufficientAIRole: false,
				ProjectScope:        "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj",
				AccountName:         "acct",
				ProjectName:         "proj",
			}, nil
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriorsForRBAC())

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Suggestion, "az role assignment create")
	require.NotEmpty(t, got.Links)
	require.Equal(t, rbacLearnLink, got.Links[0])
}

// ---- sanitizeScopeARNs ----

// TestSanitizeScopeARNs pins the regex-based scope + GUID scrubber
// used in the probe-error path. Covers the leak vectors enumerated
// in the Opus xhigh + GPT-5.5 reviews of commit 0c4d5ee31:
//   - Full ARM URL from azcore.ResponseError.Error()
//   - Bare ARM resource ID embedded in prose
//   - Subscription-only scope (/subscriptions/<guid>)
//   - Bare GUID outside any scope path
//   - Mixed text with multiple scopes
func TestSanitizeScopeARNs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		out  string
	}{
		{
			name: "full ARM URL from azcore ResponseError",
			in: "GET https://management.azure.com/subscriptions/" +
				"11111111-1111-1111-1111-111111111111/resourceGroups/rg/" +
				"providers/Microsoft.Authorization/roleAssignments -> 500",
			out: "GET https://management.azure.com<redacted> -> 500",
		},
		{
			name: "bare ARM resource ID in prose",
			in: "could not list role assignments at scope " +
				"/subscriptions/abc/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/acct: 403",
			out: "could not list role assignments at scope <redacted>: 403",
		},
		{
			name: "subscription-only scope",
			in:   "denied at /subscriptions/abc",
			out:  "denied at <redacted>",
		},
		{
			name: "bare GUID outside a scope path",
			in: "principal 22222222-2222-2222-2222-222222222222 " +
				"does not have access",
			out: "principal <redacted> does not have access",
		},
		{
			name: "no sensitive substrings - pass-through",
			in:   "dial tcp: i/o timeout",
			out:  "dial tcp: i/o timeout",
		},
		{
			name: "idempotent on already-redacted text",
			in:   "denied at <redacted>",
			out:  "denied at <redacted>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.out, sanitizeScopeARNs(tc.in))
		})
	}
}

// ---- redaction helpers ----

func TestRedactHelpers_RedactWhenFlagOff(t *testing.T) {
	t.Parallel()

	require.Equal(t, redactedPlaceholder, redactID("oid-123", false))
	require.Equal(t, redactedPlaceholder, redactScope("/subscriptions/sub", false))
	require.Equal(t, redactedPlaceholder, redactDisplay("Alice Example", false))
}

func TestRedactHelpers_PassthroughWhenFlagOn(t *testing.T) {
	t.Parallel()

	require.Equal(t, "oid-123", redactID("oid-123", true))
	require.Equal(t, "/subscriptions/sub", redactScope("/subscriptions/sub", true))
	require.Equal(t, "Alice Example", redactDisplay("Alice Example", true))
}

func TestRedactHelpers_EmptyInputIsAlwaysEmpty(t *testing.T) {
	t.Parallel()

	// An empty input represents a missing field (Graph didn't
	// return it); the helper should NOT substitute the placeholder
	// there because that would falsely imply a value was present.
	require.Empty(t, redactID("", false))
	require.Empty(t, redactScope("", false))
	require.Empty(t, redactDisplay("", false))
	require.Empty(t, redactID("", true))
	require.Empty(t, redactScope("", true))
	require.Empty(t, redactDisplay("", true))
}

// ---- Sanity check: token / OID must not leak through the Suggestion
// when the placeholder substitution path is exercised ----

func TestClassifyRBACResult_RedactedSuggestionDoesNotLeakIdentifiers(t *testing.T) {
	t.Parallel()

	got := classifyRBACResult(&project.DeveloperRBACResult{
		PrincipalID:         "extremely-secret-oid-1234567890",
		PrincipalDisplay:    "Alice Example",
		HasSufficientAIRole: false,
		ProjectScope:        "/subscriptions/super-secret-sub/rg/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj",
		AccountName:         "acct",
		ProjectName:         "proj",
	}, false)

	require.NotContains(t, got.Suggestion, "extremely-secret-oid-1234567890")
	require.NotContains(t, got.Suggestion, "super-secret-sub")
	// The Message renders short-form identifiers ("acct/proj") so
	// the account/project names ARE allowed — they are part of the
	// human-readable summary, not sensitive scope ARNs. But the
	// raw scope must not leak.
	require.NotContains(t, got.Message, "super-secret-sub")
}

// ---- Default-wiring sanity ----
//
// The two production defaults (readProjectResourceID and
// project.QueryDeveloperRBAC) are wired via a `nil`-then-fallback
// pattern inside newCheckRBAC. They depend on a live gRPC channel
// and real ARM/Graph stacks; a unit-test panic-safe driver would
// require a substantial fake-client harness that this package does
// not yet provide. The fallback wiring is verified by code review
// (single-line `if probe == nil { probe = project.QueryDeveloperRBAC }`)
// and by `TestCheckRBAC_PassesWhenProbeReturnsRole` / `TestCheckRBAC_FailsWhenProbeReturnsNoRole`
// exercising the surrounding flow with the seams in place.
