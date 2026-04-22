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
