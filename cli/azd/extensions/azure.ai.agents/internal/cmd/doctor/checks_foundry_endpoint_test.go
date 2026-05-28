// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package doctor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// foundryProbeStub builds a Dependencies whose probeFoundryEndpoint
// seam returns a fixed foundryProbeResult and an AgentAPIVersion of
// `v1`. Centralised so every status-code test reads
// at the same level of abstraction.
func foundryProbeStub(res foundryProbeResult) Dependencies {
	return Dependencies{
		AgentAPIVersion: "v1",
		probeFoundryEndpoint: func(_ context.Context, _ string) foundryProbeResult {
			return res
		},
	}
}

// passingPriors returns the upstream prior results that the foundry-
// endpoint check requires to actually run: a Pass for both
// `local.project-endpoint-set` (with the endpoint string in Details)
// and `remote.auth`.
func passingPriors(endpoint string) []Result {
	return []Result{
		{ID: "local.environment-selected", Status: StatusPass},
		{
			ID:     "local.project-endpoint-set",
			Status: StatusPass,
			Details: map[string]any{
				"projectEndpoint": endpoint,
			},
		},
		{ID: "remote.auth", Status: StatusPass},
	}
}

// ---- Skip-cascade contract ----

func TestCheckFoundryEndpoint_SkipsWhenProjectEndpointSetFailed(t *testing.T) {
	t.Parallel()

	check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
		err: errors.New("probe should not have been called"),
	}))
	prior := []Result{
		{ID: "local.project-endpoint-set", Status: StatusFail},
		{ID: "remote.auth", Status: StatusPass},
	}

	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "FOUNDRY_PROJECT_ENDPOINT")
	require.Contains(t, got.Message, "local.project-endpoint-set")
}

func TestCheckFoundryEndpoint_SkipsWhenAuthFailed(t *testing.T) {
	t.Parallel()

	check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
		err: errors.New("probe should not have been called"),
	}))
	prior := []Result{
		{
			ID:     "local.project-endpoint-set",
			Status: StatusPass,
			Details: map[string]any{
				"projectEndpoint": "https://acct.services.ai.azure.com/api/projects/proj",
			},
		},
		{ID: "remote.auth", Status: StatusFail},
	}

	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "auth probe")
	require.Contains(t, got.Message, "remote.auth")
}

func TestCheckFoundryEndpoint_SkipsWhenEndpointMissingFromDetails(t *testing.T) {
	t.Parallel()

	check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
		err: errors.New("probe should not have been called"),
	}))
	prior := []Result{
		{
			ID:      "local.project-endpoint-set",
			Status:  StatusPass,
			Details: map[string]any{}, // missing projectEndpoint key
		},
		{ID: "remote.auth", Status: StatusPass},
	}

	got := check.Fn(t.Context(), Options{}, prior)

	require.Equal(t, StatusSkip, got.Status,
		"defensive skip — must not guess endpoint values")
	require.Contains(t, got.Message, "Details")
}

func TestCheckFoundryEndpoint_SkipsWhenAPIVersionMissing(t *testing.T) {
	t.Parallel()

	// No AgentAPIVersion populated on Dependencies — wiring bug.
	check := newCheckFoundryEndpoint(Dependencies{
		probeFoundryEndpoint: func(_ context.Context, _ string) foundryProbeResult {
			return foundryProbeResult{err: errors.New("probe should not have been called")}
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriors("https://x.services.ai.azure.com/api/projects/p"))

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "agent API version")
}

// ---- Status-code → Result mapping (the heart of the check) ----

func TestCheckFoundryEndpoint_PassesOn200(t *testing.T) {
	t.Parallel()

	endpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
		statusCode:   http.StatusOK,
		requestedURL: endpoint + "/agents?api-version=v1&limit=1",
	}))

	got := check.Fn(t.Context(), Options{}, passingPriors(endpoint))

	require.Equal(t, StatusPass, got.Status)
	require.Contains(t, got.Message, "endpoint reachable")
	require.Contains(t, got.Message, "HTTP 200")
	require.Equal(t, endpoint, got.Details["endpoint"])
	require.Equal(t, http.StatusOK, got.Details["statusCode"])
}

func TestCheckFoundryEndpoint_FailsOn401WithAzdAuthLogin(t *testing.T) {
	t.Parallel()

	endpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
		statusCode: http.StatusUnauthorized,
	}))

	got := check.Fn(t.Context(), Options{}, passingPriors(endpoint))

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "401")
	require.Contains(t, got.Message, "token expired")
	require.Contains(t, got.Suggestion, "azd auth login")
	require.NotEmpty(t, got.Links)
}

func TestCheckFoundryEndpoint_FailsOn403WithTenantOrRBAC(t *testing.T) {
	t.Parallel()

	endpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
		statusCode: http.StatusForbidden,
	}))

	got := check.Fn(t.Context(), Options{}, passingPriors(endpoint))

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "403")
	require.Contains(t, got.Message, "wrong tenant")
	require.Contains(t, got.Message, "insufficient RBAC")
	require.Contains(t, got.Suggestion, "tenant")
	require.Contains(t, got.Suggestion, "remote.rbac")
	require.NotContains(t, got.Suggestion, "azd auth login",
		"403 must NOT recommend `azd auth login` — that's the 401 path")
	require.NotEmpty(t, got.Links,
		"403 must carry a docs Link so users have somewhere to start "+
			"acting on the suggestion (mirrors the C11 convention)")
}

func TestCheckFoundryEndpoint_FailsOn404WithProvisionFix(t *testing.T) {
	t.Parallel()

	endpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
		statusCode: http.StatusNotFound,
	}))

	got := check.Fn(t.Context(), Options{}, passingPriors(endpoint))

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "404")
	require.Contains(t, got.Message, "endpoint is wrong")
	require.Contains(t, got.Suggestion, "azd provision")
	require.Contains(t, got.Suggestion, "azd env set FOUNDRY_PROJECT_ENDPOINT")
}

func TestCheckFoundryEndpoint_FailsOnServerError(t *testing.T) {
	t.Parallel()

	cases := []int{500, 502, 503, 504, 599}
	for _, code := range cases {
		t.Run(http.StatusText(code), func(t *testing.T) {
			t.Parallel()
			endpoint := "https://acct.services.ai.azure.com/api/projects/proj"
			check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
				statusCode: code,
			}))

			got := check.Fn(t.Context(), Options{}, passingPriors(endpoint))

			require.Equal(t, StatusFail, got.Status)
			require.Contains(t, got.Message, "service-side error")
			require.Contains(t, got.Suggestion, "Retry")
		})
	}
}

func TestCheckFoundryEndpoint_FailsOnUnexpectedStatus(t *testing.T) {
	t.Parallel()

	endpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
		statusCode: http.StatusTeapot,
	}))

	got := check.Fn(t.Context(), Options{}, passingPriors(endpoint))

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "unexpected HTTP 418")
	require.Contains(t, got.Suggestion, "verbose")
}

func TestCheckFoundryEndpoint_FailsOnTransportError(t *testing.T) {
	t.Parallel()

	endpoint := "https://typo.services.ai.azure.com/api/projects/proj"
	check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
		err: errors.New("dial tcp: lookup typo.services.ai.azure.com: no such host"),
	}))

	got := check.Fn(t.Context(), Options{}, passingPriors(endpoint))

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "could not reach")
	require.Contains(t, got.Message, "typo.services.ai.azure.com")
	require.Contains(t, got.Suggestion, "FOUNDRY_PROJECT_ENDPOINT")
}

func TestCheckFoundryEndpoint_StripsMultiLineTransportError(t *testing.T) {
	t.Parallel()

	endpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	multi := "primary cause\nstack frame 1\nstack frame 2"
	check := newCheckFoundryEndpoint(foundryProbeStub(foundryProbeResult{
		err: errors.New(multi),
	}))

	got := check.Fn(t.Context(), Options{}, passingPriors(endpoint))

	require.Contains(t, got.Message, "primary cause")
	require.NotContains(t, got.Message, "stack frame")
}

// ---- Cancellation / timeout ----

func TestCheckFoundryEndpoint_SkipsOnUserCancellation(t *testing.T) {
	t.Parallel()

	endpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	check := newCheckFoundryEndpoint(Dependencies{
		AgentAPIVersion: "v1",
		probeFoundryEndpoint: func(ctx context.Context, _ string) foundryProbeResult {
			<-ctx.Done()
			return foundryProbeResult{err: ctx.Err()}
		},
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got := check.Fn(ctx, Options{}, passingPriors(endpoint))

	require.Equal(t, StatusSkip, got.Status)
	require.Contains(t, got.Message, "cancelled")
}

func TestCheckFoundryEndpoint_FailsOnProbeTimeout(t *testing.T) {
	t.Parallel()

	endpoint := "https://acct.services.ai.azure.com/api/projects/proj"
	check := newCheckFoundryEndpoint(Dependencies{
		AgentAPIVersion: "v1",
		probeFoundryEndpoint: func(_ context.Context, _ string) foundryProbeResult {
			return foundryProbeResult{err: context.DeadlineExceeded}
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriors(endpoint))

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "did not respond within")
	require.Contains(t, got.Suggestion, "retry")
}

// ---- Default probe fall-through ----

func TestCheckFoundryEndpoint_FallsBackToRealProbeWhenSeamMissing(t *testing.T) {
	t.Parallel()

	// With probeFoundryEndpoint nil the check must call
	// makeRealProbeFoundryEndpoint. We feed an already-cancelled ctx
	// so the underlying http.DefaultClient call returns immediately
	// regardless of the host's network state, and assert the
	// cancellation classification kicks in.
	check := newCheckFoundryEndpoint(Dependencies{
		AgentAPIVersion: "v1",
	})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got := check.Fn(ctx, Options{}, passingPriors("https://x.services.ai.azure.com/api/projects/p"))

	// Either Skip (cancellation classified) or Fail (timeout / token
	// acquisition error) is acceptable — we only need to prove the
	// fall-through doesn't panic on a nil probe and produces a
	// classified Result.
	require.Contains(t, []Status{StatusSkip, StatusFail}, got.Status)
}

// ---- End-to-end via a real httptest server (verifies the
// production probe assembly produces the expected HTTP request
// against a real net/http stack) ----

func TestRealProbeFoundryEndpoint_RequestShapeAgainstHTTPTestServer(t *testing.T) {
	t.Parallel()

	// We can't easily inject a stub credential into
	// makeRealProbeFoundryEndpoint without expanding its surface, so
	// we exercise the *URL builder* end-to-end against an httptest
	// TLS server (the production probe contract requires HTTPS, so a
	// plaintext httptest server would be rejected by our own URL
	// validation). This catches regressions where the builder
	// produces a URL that fails to land on `/agents` once Go's
	// net/http library has had a chance to canonicalize / send it —
	// a class of failure that pure string assertions miss.
	var seenPath, seenQuery string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got, err := buildFoundryProbeURL(srv.URL, "v1")
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, got, nil)
	require.NoError(t, err)
	// Use the test server's pre-configured client (trusts the
	// httptest CA) instead of http.DefaultClient.
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	require.Equal(t, "/agents", seenPath,
		"the built URL must resolve to /agents on the wire")
	require.Contains(t, seenQuery, "api-version=v1")
	require.Contains(t, seenQuery, "limit=1",
		"the probe must use limit=1 (matches production "+
			"agent_api/operations.go) — not $top=1")
}

// ---- buildFoundryProbeURL ----

func TestBuildFoundryProbeURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		endpoint     string
		wantContains []string // substrings that MUST appear
		wantMissing  []string // substrings that MUST NOT appear
	}{
		{
			name:     "no trailing slash",
			endpoint: "https://x.services.ai.azure.com/api/projects/proj",
			wantContains: []string{
				"https://x.services.ai.azure.com/api/projects/proj/agents?",
				"api-version=v1",
				"limit=1",
			},
		},
		{
			name:     "trailing slash tolerated",
			endpoint: "https://x.services.ai.azure.com/api/projects/proj/",
			wantContains: []string{
				"https://x.services.ai.azure.com/api/projects/proj/agents?",
				"limit=1",
			},
		},
		{
			name:     "user-supplied junk query is overridden but path survives",
			endpoint: "https://x.services.ai.azure.com/api/projects/proj?api-version=evil&injected=x",
			wantContains: []string{
				"/api/projects/proj/agents?",
				"api-version=v1",
				"limit=1",
			},
			wantMissing: []string{"api-version=evil", "injected=x"},
		},
		{
			name:     "fragment stripped and path survives",
			endpoint: "https://x.services.ai.azure.com/api/projects/proj#evil/agents",
			wantContains: []string{
				"/api/projects/proj/agents?",
				"api-version=v1",
				"limit=1",
			},
			wantMissing: []string{"#"},
		},
		{
			name:     "whitespace trimmed",
			endpoint: "   https://x.services.ai.azure.com/api/projects/proj   ",
			wantContains: []string{
				"https://x.services.ai.azure.com/api/projects/proj/agents?",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := buildFoundryProbeURL(tc.endpoint, "v1")
			require.NoError(t, err)
			for _, sub := range tc.wantContains {
				require.Containsf(t, got, sub, "URL %q missing substring %q", got, sub)
			}
			for _, sub := range tc.wantMissing {
				require.NotContainsf(t, got, sub,
					"URL %q must not contain %q", got, sub)
			}
			// Universal positive assertion: every successful build
			// must include the literal /agents path segment with
			// the canonical query separator immediately after. This
			// is the regression test for the path-loss bug uncovered
			// by reviewers: a builder that silently dropped /agents
			// (via ?- or #-collision) would have passed every
			// per-case `wantContains` list above before this line
			// existed.
			require.Contains(t, got, "/agents?",
				"every built URL must include `/agents?` — never let "+
					"a user-supplied query/fragment displace the path")
		})
	}
}

func TestBuildFoundryProbeURL_RejectsNonHTTPSOrMalformedEndpoint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		endpoint string
	}{
		{name: "http scheme", endpoint: "http://x.services.ai.azure.com/api/projects/proj"},
		{name: "no scheme", endpoint: "x.services.ai.azure.com/api/projects/proj"},
		{name: "opaque string", endpoint: "not a url"},
		{name: "empty", endpoint: ""},
		{name: "relative path", endpoint: "/api/projects/proj"},
		{name: "ftp scheme", endpoint: "ftp://x.services.ai.azure.com/api/projects/proj"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := buildFoundryProbeURL(tc.endpoint, "v1")
			require.Error(t, err,
				"builder must reject non-HTTPS / relative / malformed "+
					"endpoints so the probe never sends a bearer token "+
					"to the wrong scheme or host")
		})
	}
}

func TestValidateFoundryEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("accepts well-formed https", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateFoundryEndpoint(
			"https://x.services.ai.azure.com/api/projects/proj"))
	})

	t.Run("accepts https with trailing slash", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, validateFoundryEndpoint(
			"https://x.services.ai.azure.com/api/projects/proj/"))
	})

	t.Run("rejects http", func(t *testing.T) {
		t.Parallel()
		err := validateFoundryEndpoint(
			"http://x.services.ai.azure.com/api/projects/proj")
		require.Error(t, err)
		require.Contains(t, err.Error(), "https")
	})

	t.Run("rejects empty", func(t *testing.T) {
		t.Parallel()
		err := validateFoundryEndpoint("   ")
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty")
	})

	t.Run("rejects relative URL", func(t *testing.T) {
		t.Parallel()
		err := validateFoundryEndpoint("/api/projects/proj")
		require.Error(t, err)
	})

	t.Run("rejects opaque non-URL string", func(t *testing.T) {
		t.Parallel()
		err := validateFoundryEndpoint("not a url")
		require.Error(t, err)
	})
}

func TestCheckFoundryEndpoint_FailsOnNonHTTPSEndpoint(t *testing.T) {
	t.Parallel()

	// Stub probe that would panic if invoked — the check must fail
	// the request at validation time, BEFORE any token is acquired
	// or any probe is dispatched.
	check := newCheckFoundryEndpoint(Dependencies{
		AgentAPIVersion: "v1",
		probeFoundryEndpoint: func(_ context.Context, _ string) foundryProbeResult {
			t.Fatal("probe must not be invoked for a non-HTTPS endpoint")
			return foundryProbeResult{}
		},
	})

	endpoint := "http://x.services.ai.azure.com/api/projects/proj"
	got := check.Fn(t.Context(), Options{}, passingPriors(endpoint))

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "invalid")
	require.Contains(t, got.Suggestion, "azd env set FOUNDRY_PROJECT_ENDPOINT")
	require.Equal(t, endpoint, got.Details["endpoint"])
	require.NotEmpty(t, got.Details["validationError"])
}

func TestCheckFoundryEndpoint_FailsOnMalformedEndpoint(t *testing.T) {
	t.Parallel()

	check := newCheckFoundryEndpoint(Dependencies{
		AgentAPIVersion: "v1",
		probeFoundryEndpoint: func(_ context.Context, _ string) foundryProbeResult {
			t.Fatal("probe must not be invoked for a malformed endpoint")
			return foundryProbeResult{}
		},
	})

	got := check.Fn(t.Context(), Options{}, passingPriors("not a url"))

	require.Equal(t, StatusFail, got.Status)
	require.Contains(t, got.Message, "invalid")
}

// ---- endpointHost / readProjectEndpoint helpers ----

func TestEndpointHost(t *testing.T) {
	t.Parallel()

	require.Equal(t, "x.services.ai.azure.com",
		endpointHost("https://x.services.ai.azure.com/api/projects/proj"))
	require.Equal(t, "not a url",
		endpointHost("not a url"),
		"on parse failure / empty host, the input is returned verbatim")
	require.Equal(t, "",
		endpointHost(""))
}

func TestReadProjectEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("returns trimmed value from Details", func(t *testing.T) {
		t.Parallel()
		got := readProjectEndpoint([]Result{
			{
				ID:      "local.project-endpoint-set",
				Details: map[string]any{"projectEndpoint": "  https://x.services.ai.azure.com  "},
			},
		})
		require.Equal(t, "https://x.services.ai.azure.com", got)
	})

	t.Run("returns empty when result missing", func(t *testing.T) {
		t.Parallel()
		got := readProjectEndpoint([]Result{
			{ID: "remote.auth"},
		})
		require.Empty(t, got)
	})

	t.Run("returns empty when value not a string", func(t *testing.T) {
		t.Parallel()
		got := readProjectEndpoint([]Result{
			{
				ID:      "local.project-endpoint-set",
				Details: map[string]any{"projectEndpoint": 12345},
			},
		})
		require.Empty(t, got)
	})

	t.Run("returns empty when Details nil", func(t *testing.T) {
		t.Parallel()
		got := readProjectEndpoint([]Result{
			{ID: "local.project-endpoint-set"},
		})
		require.Empty(t, got)
	})
}

// ---- foundryDetails ----

func TestFoundryDetails_OmitsZeroStatusAndEmptyURL(t *testing.T) {
	t.Parallel()

	d := foundryDetails("https://x", foundryProbeResult{})
	require.Equal(t, "https://x", d["endpoint"])
	_, hasStatus := d["statusCode"]
	require.False(t, hasStatus, "zero statusCode should not appear in Details")
	_, hasURL := d["requestedURL"]
	require.False(t, hasURL, "empty requestedURL should not appear in Details")
}

func TestFoundryDetails_IncludesStatusAndURLWhenSet(t *testing.T) {
	t.Parallel()

	d := foundryDetails("https://x", foundryProbeResult{
		statusCode:   200,
		requestedURL: "https://x/agents?api-version=v1&limit=1",
	})
	require.Equal(t, 200, d["statusCode"])
	require.Contains(t, d["requestedURL"], "/agents")
}

// ---- Sanity check: token must not leak via Details ----

func TestFoundryDetails_NeverContainsToken(t *testing.T) {
	t.Parallel()

	// The probe surface is intentionally narrow — there is no token
	// field on foundryProbeResult. This test pins that contract: if
	// someone adds one in the future, foundryDetails must still
	// refuse to surface it.
	d := foundryDetails("https://x", foundryProbeResult{
		statusCode:   200,
		requestedURL: "https://x/agents?api-version=v1",
	})
	for k, v := range d {
		require.NotContains(t, strings.ToLower(k), "token",
			"Details key %q looks token-related", k)
		if s, ok := v.(string); ok {
			require.NotContains(t, strings.ToLower(s), "bearer ",
				"Details value contains what looks like a bearer token")
		}
	}
}
