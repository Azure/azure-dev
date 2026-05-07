// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/stretchr/testify/require"
)

const samlDocURL = "https://docs.github.com/articles/" +
	"authenticating-to-a-github-organization-with-saml-single-sign-on/"

const samlStdoutBody = `{"message":"Resource protected by organization SAML enforcement. ` +
	`You must grant your OAuth token access to this organization.",` +
	`"documentation_url":"` + samlDocURL + `",` +
	`"status":"403"}`

const samlStderrLine = "gh: Resource protected by organization SAML enforcement. " +
	"You must grant your OAuth token access to this organization. (HTTP 403)"

func TestParseApiError_NilError(t *testing.T) {
	t.Parallel()
	require.Nil(t, parseApiError("https://api.github.com/x", "", "", nil))
}

func TestParseApiError_StatusFromJSONBody(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		stdout string
		stderr string
		want   int
	}{
		{
			"401 from JSON",
			`{"message":"Bad credentials","documentation_url":"...","status":"401"}`,
			"gh: Bad credentials (HTTP 401)",
			401,
		},
		{
			"403 SAML from JSON",
			samlStdoutBody,
			samlStderrLine,
			403,
		},
		{
			// Plain 403 without SAML/rate-limit phrases must classify as
			// KindForbidden (asserted in TestClassifyKind_HttpStatusToKind).
			"403 plain forbidden",
			`{"message":"Resource not accessible by integration","documentation_url":"...","status":"403"}`,
			"gh: Resource not accessible by integration (HTTP 403)",
			403,
		},
		{
			"500 server error from JSON",
			`{"message":"Server Error","documentation_url":"...","status":"500"}`,
			"gh: Server Error (HTTP 500)",
			500,
		},
		{
			"422 unprocessable (KindOther bucket)",
			`{"message":"Validation Failed","documentation_url":"...","status":"422"}`,
			"gh: Validation Failed (HTTP 422)",
			422,
		},
		{
			"404 from JSON",
			`{"message":"Not Found","documentation_url":"...","status":"404"}`,
			"gh: Not Found (HTTP 404)",
			404,
		},
		{
			"stderr fallback when stdout has no JSON",
			"",
			"gh: Server error (HTTP 500)",
			500,
		},
		{
			"stderr fallback when stdout JSON missing fields",
			`{"foo":"bar"}`,
			"gh: weird (HTTP 502)",
			502,
		},
		{
			// Truncated/invalid JSON (e.g., proxy returning HTML, gh
			// crashing mid-write). We must not panic on json.Unmarshal
			// and must still recover the status code from stderr.
			"malformed JSON falls back to stderr",
			`{"message":"oops`,
			"gh: oops (HTTP 503)",
			503,
		},
		{
			// Proxy-style HTML response: not JSON at all. The early
			// "not a JSON object" guard short-circuits without invoking
			// the unmarshaler, and stderr still recovers the status.
			"HTML body falls back to stderr",
			"<html><body>Bad gateway</body></html>",
			"gh: Bad gateway (HTTP 502)",
			502,
		},
		{
			// GitHub error bodies often include only message+documentation_url
			// (no status). We must still capture the message and recover the
			// HTTP code from stderr's "(HTTP NNN)" marker.
			"message-only JSON falls back to stderr for status",
			`{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest"}`,
			"gh: Bad credentials (HTTP 401)",
			401,
		},
		{
			"no marker anywhere",
			"",
			"gh: failed talking to api/repos/team-401k",
			0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := errors.New("exit 1")
			apiErr := parseApiError("https://api.github.com/r", tc.stdout, tc.stderr, err)
			require.NotNil(t, apiErr)
			require.Equal(t, tc.want, apiErr.StatusCode)
		})
	}
}

func TestParseApiError_DetectsSAML(t *testing.T) {
	t.Parallel()
	apiErr := parseApiError(
		"https://api.github.com/repos/o/r",
		samlStdoutBody,
		samlStderrLine,
		errors.New("exit 1"),
	)
	require.NotNil(t, apiErr)
	require.Equal(t, KindSAMLBlocked, apiErr.Kind)
	require.True(t, apiErr.IsAuthError())
	require.Equal(t, 403, apiErr.StatusCode)
	require.Contains(t, apiErr.Message, "SAML enforcement")
}

func TestParseApiError_DetectsRateLimit(t *testing.T) {
	t.Parallel()
	apiErr := parseApiError(
		"https://api.github.com/repos/o/r",
		`{"message":"API rate limit exceeded for user ID 12345.","documentation_url":"...","status":"403"}`,
		"gh: API rate limit exceeded for user ID 12345. (HTTP 403)",
		errors.New("exit 1"),
	)
	require.NotNil(t, apiErr)
	require.Equal(t, KindRateLimited, apiErr.Kind)
	require.Equal(t, 403, apiErr.StatusCode)
}

func TestParseApiError_DoesNotMisclassifySsoInRepoName(t *testing.T) {
	t.Parallel()
	// Repo name "sso-tools" must not trip SAML detection: we look for
	// specific phrases ("saml enforcement", "saml sso", "sso authorization",
	// "sso required"), not a bare "sso" substring.
	apiErr := parseApiError(
		"https://api.github.com/repos/o/sso-tools/branches/main",
		`{"message":"Not Found","documentation_url":"...","status":"404"}`,
		"gh: Not Found (HTTP 404) at /repos/o/sso-tools/branches/main",
		errors.New("exit 1"),
	)
	require.NotNil(t, apiErr)
	require.Equal(t, KindNotFound, apiErr.Kind)
	require.False(t, apiErr.IsAuthError())
	require.True(t, apiErr.IsNotFound())
}

func TestParseApiError_MessageOnlyBodyCapturesMessage(t *testing.T) {
	t.Parallel()
	// Verify the diagnostic Message is captured even when the JSON body
	// omits "status" — a common shape for GitHub REST errors. The HTTP code
	// still comes from the stderr "(HTTP NNN)" marker.
	apiErr := parseApiError(
		"https://api.github.com/repos/o/r",
		`{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest"}`,
		"gh: Bad credentials (HTTP 401)",
		errors.New("exit 1"),
	)
	require.NotNil(t, apiErr)
	require.Equal(t, "Bad credentials", apiErr.Message)
	require.Equal(t, 401, apiErr.StatusCode)
	require.Equal(t, KindUnauthorized, apiErr.Kind)
}

// TestClassifyKind_HttpStatusToKind exercises the full status-code → Kind
// mapping in classifyKind, including the buckets (401/403/404/5xx/other)
// and the SAML / rate-limit phrase overrides on a 403. Each row uses
// realistic stderr/JSON shapes from gh CLI.
func TestClassifyKind_HttpStatusToKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		text   string
		want   ApiErrorKind
	}{
		{"401 → Unauthorized", 401, "Bad credentials", KindUnauthorized},
		{"403 plain → Forbidden", 403, "Resource not accessible by integration", KindForbidden},
		{"403 SAML → SAMLBlocked", 403,
			"Resource protected by organization SAML enforcement", KindSAMLBlocked},
		{"403 secondary rate limit → RateLimited", 403,
			"You have exceeded a secondary rate limit", KindRateLimited},
		{"403 primary rate limit → RateLimited", 403,
			"API rate limit exceeded for user ID 12345", KindRateLimited},
		{"404 → NotFound", 404, "Not Found", KindNotFound},
		{"422 → Other", 422, "Validation Failed", KindOther},
		{"409 → Other", 409, "Conflict", KindOther},
		{"500 → ServerError", 500, "Server Error", KindServerError},
		{"502 → ServerError", 502, "Bad Gateway", KindServerError},
		{"503 → ServerError", 503, "Service Unavailable", KindServerError},
		{"0 (no status) → Unknown", 0, "", KindUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, classifyKind(tc.status, tc.text))
		})
	}
}

// TestClassifyKind_SAMLPhraseVariants verifies all phrase patterns the
// SAML detector recognizes. Adding a new phrase to classifyKind should be
// accompanied by a new row here.
func TestClassifyKind_SAMLPhraseVariants(t *testing.T) {
	t.Parallel()
	phrases := []string{
		"Resource protected by organization SAML enforcement",
		"You must use SAML SSO before accessing this resource",
		"Your token has not been granted SSO authorization for this organization",
		"SSO required for this organization",
		// Casing must not matter — classifier lowercases the input first.
		"resource protected by organization saml enforcement",
		"SAML SSO REQUIRED",
	}
	for _, p := range phrases {
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, KindSAMLBlocked, classifyKind(403, p))
		})
	}
}

// TestParseApiError_StderrHttpStatusFallback exercises the "(HTTP NNN)"
// regex fallback that recovers the status code from gh's stderr when the
// JSON body on stdout is missing/unusable. Documents what the regex does
// and does NOT match (3-digit form only, first match wins).
func TestParseApiError_StderrHttpStatusFallback(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		stderr     string
		wantStatus int
		wantKind   ApiErrorKind
	}{
		{
			name:       "standard gh suffix",
			stderr:     "gh: Not Found (HTTP 404)",
			wantStatus: 404,
			wantKind:   KindNotFound,
		},
		{
			name:       "extra trailing text after marker",
			stderr:     "gh: Bad credentials (HTTP 401)\nTry running gh auth login",
			wantStatus: 401,
			wantKind:   KindUnauthorized,
		},
		{
			name:       "first marker wins when multiple are present",
			stderr:     "gh: outer (HTTP 502) inner (HTTP 500)",
			wantStatus: 502,
			wantKind:   KindServerError,
		},
		{
			name:       "no marker present",
			stderr:     "gh: connection refused",
			wantStatus: 0,
			wantKind:   KindUnknown,
		},
		{
			name:       "extra whitespace inside parens does not match",
			stderr:     "gh: weird ( HTTP 500 )",
			wantStatus: 0,
			wantKind:   KindUnknown,
		},
		{
			name:       "two-digit code does not match (regex requires 3 digits)",
			stderr:     "gh: weird (HTTP 99)",
			wantStatus: 0,
			wantKind:   KindUnknown,
		},
		{
			name:       "lowercase 'http' does not match (case sensitive)",
			stderr:     "gh: weird (http 500)",
			wantStatus: 0,
			wantKind:   KindUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Empty stdout forces reliance on the stderr fallback only.
			apiErr := parseApiError("https://api.github.com/x", "", tc.stderr, errors.New("gh failed"))
			require.NotNil(t, apiErr)
			require.Equal(t, tc.wantStatus, apiErr.StatusCode)
			require.Equal(t, tc.wantKind, apiErr.Kind)
		})
	}
}

func TestParseApiError_NoOutputAvailable(t *testing.T) {
	t.Parallel()
	// gh failed before issuing the request (network error, missing binary,
	// ctx cancelled, etc.) — we still return a non-nil ApiError with
	// StatusCode=0 so callers' errors.AsType[*ApiError] checks succeed.
	underlying := errors.New("dial tcp: lookup api.github.com: no such host")
	apiErr := parseApiError("https://api.github.com/repos/o/r", "", "", underlying)
	require.NotNil(t, apiErr)
	require.Equal(t, KindUnknown, apiErr.Kind)
	require.Equal(t, 0, apiErr.StatusCode)
	require.Same(t, underlying, apiErr.Underlying)
}

func TestApiError_UnwrapPreservesChain(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	apiErr := &ApiError{URL: "u", Underlying: sentinel}
	require.ErrorIs(t, apiErr, sentinel)
}

func TestApiError_ErrorMessage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  *ApiError
		want string
	}{
		{
			"saml",
			&ApiError{URL: "https://api.github.com/x", StatusCode: 403, Kind: KindSAMLBlocked},
			"gh api https://api.github.com/x: SAML SSO enforcement blocked the request (HTTP 403)",
		},
		{
			"rate limit",
			&ApiError{URL: "https://api.github.com/x", StatusCode: 403, Kind: KindRateLimited},
			"gh api https://api.github.com/x: GitHub API rate limit exceeded (HTTP 403)",
		},
		{
			"plain status with message",
			&ApiError{URL: "https://api.github.com/x", StatusCode: 404, Kind: KindNotFound, Message: "Not Found"},
			"gh api https://api.github.com/x: HTTP 404: Not Found",
		},
		{
			"plain status without message",
			&ApiError{URL: "https://api.github.com/x", StatusCode: 500, Kind: KindServerError},
			"gh api https://api.github.com/x: HTTP 500",
		},
		{
			"unknown kind falls back to underlying",
			&ApiError{URL: "https://api.github.com/x", Kind: KindUnknown, Underlying: errors.New("boom")},
			"gh api https://api.github.com/x: boom",
		},
		{
			"unknown kind with nil underlying",
			&ApiError{URL: "https://api.github.com/x", Kind: KindUnknown},
			"gh api https://api.github.com/x: unknown error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.Error())
		})
	}
}

// Integration-style: ApiCall returns *ApiError on failure with status
// extracted from the captured stdout JSON body.
func TestApiCall_ReturnsTypedApiErrorOnFailure(t *testing.T) {
	t.Parallel()
	cli, mockCtx := newTestCli(t)
	mockCtx.CommandRunner.When(
		func(args exec.RunArgs, _ string) bool {
			return len(args.Args) > 0 && args.Args[0] == "api"
		},
	).RespondFn(func(_ exec.RunArgs) (exec.RunResult, error) {
		stdout := `{"message":"Bad credentials","documentation_url":"...","status":"401"}`
		stderr := "gh: Bad credentials (HTTP 401)"
		return exec.NewRunResult(1, stdout, stderr),
			fmt.Errorf("exit code: 1, stdout: %s, stderr: %s", stdout, stderr)
	})

	_, err := cli.ApiCall(t.Context(), "github.com", "/repos/o/r", ApiCallOptions{})
	require.Error(t, err)
	apiErr, ok := errors.AsType[*ApiError](err)
	require.True(t, ok, "expected error to unwrap to *ApiError, got: %v", err)
	require.Equal(t, 401, apiErr.StatusCode)
	require.True(t, apiErr.IsAuthError())
	require.Equal(t, "Bad credentials", apiErr.Message)
}
