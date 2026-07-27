// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	msal "github.com/AzureAD/microsoft-authentication-library-for-go/apps/errors"
	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/public"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
)

func TestAuthFailedError_NonMsalCallError(t *testing.T) {
	err := newAuthFailedErrorFromMsalErr(errors.New("some error"))
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "some error")
}

func TestAuthFailedError(t *testing.T) {
	marshal := func(e *AadErrorResponse) string {
		b, err := json.Marshal(e)
		if err != nil {
			panic(err)
		}
		return string(b)
	}

	respWithBody := func(body string) *http.Response {
		return &http.Response{
			StatusCode: 403,
			Status:     "403 Forbidden",
			Body:       io.NopCloser(bytes.NewReader([]byte(body))),
			Request: &http.Request{
				Method: "GET",
				URL: &url.URL{
					Scheme: "https",
					Host:   "localhost",
					Path:   "/token",
				},
			},
		}
	}

	tests := []struct {
		name string
		e    error
		want string
	}{
		{
			name: "Parsed_Error",
			e: msal.CallErr{
				Resp: respWithBody(marshal(&AadErrorResponse{
					Error:            "invalid_request",
					ErrorDescription: "invalid scope in claims",
				})),
			},
			want: "(invalid_request) invalid scope in claims",
		},
		{
			name: "NotParsed_InvalidJson",
			e: msal.CallErr{
				Resp: respWithBody("error body"),
			},
			want: "error body",
		},
		{
			name: "NotParsed_EmptyBody",
			e: msal.CallErr{
				Resp: respWithBody(""),
			},
			want: "GET https://localhost/token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newAuthFailedErrorFromMsalErr(tt.e)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestNewActionableAuthError_RecognizesLoginRequiredErrors(t *testing.T) {
	tests := []struct {
		name string
		resp *AadErrorResponse
		want bool
	}{
		{"invalid_grant", &AadErrorResponse{Error: "invalid_grant"}, true},
		{"interaction_required", &AadErrorResponse{Error: "interaction_required"}, true},
		{"invalid_claim", &AadErrorResponse{Error: "invalid_claim"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := newActionableAuthError(tt.resp, LoginScopes(cloud.AzurePublic()), cloud.AzurePublic(), "", nil)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNewActionableAuthError_PreservesUnderlyingErrorText(t *testing.T) {
	tests := []struct {
		name string
		resp *AadErrorResponse
		want string
	}{
		{
			"invalid_grant",
			&AadErrorResponse{
				Error:            "invalid_grant",
				ErrorDescription: "description 1",
			},
			"description 1",
		},
		{
			"interaction_required",
			&AadErrorResponse{
				Error:            "interaction_required",
				ErrorDescription: "description 2",
			},
			"description 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err, _ := newActionableAuthError(tt.resp, LoginScopes(cloud.AzurePublic()), cloud.AzurePublic(), "", nil)
			got := err.Error()
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTokenProtectionBlockedSuggestion(t *testing.T) {
	graphScopes := []string{"https://graph.microsoft.com/.default"}
	armScopes := LoginScopes(cloud.AzurePublic())

	tests := []struct {
		name        string
		resp        *AadErrorResponse
		scopes      []string
		want        bool
		wantMessage string
	}{
		{
			name:   "nil_response",
			resp:   nil,
			scopes: armScopes,
			want:   false,
		},
		{
			name:   "no_token_protection_code",
			resp:   &AadErrorResponse{Error: "invalid_grant", ErrorCodes: []int{70043}},
			scopes: armScopes,
			want:   false,
		},
		{
			name: "token_protection_arm_scope",
			resp: &AadErrorResponse{
				Error:            "invalid_grant",
				ErrorDescription: "AADSTS530084: blocked by token protection",
				ErrorCodes:       []int{530084},
			},
			scopes:      armScopes,
			want:        true,
			wantMessage: "A Conditional Access token protection policy blocked this token request.",
		},
		{
			name: "token_protection_graph_scope",
			resp: &AadErrorResponse{
				Error:            "invalid_grant",
				ErrorDescription: "AADSTS530084: blocked by token protection",
				ErrorCodes:       []int{530084},
			},
			scopes:      graphScopes,
			want:        true,
			wantMessage: "A Conditional Access token protection policy blocked this Microsoft Graph token request.",
		},
		{
			name: "token_protection_with_other_codes",
			resp: &AadErrorResponse{
				Error:            "invalid_grant",
				ErrorDescription: "AADSTS530084: blocked by token protection",
				ErrorCodes:       []int{70043, 530084},
			},
			scopes:      armScopes,
			want:        true,
			wantMessage: "A Conditional Access token protection policy blocked this token request.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var failed *AuthFailedError
			if tt.resp != nil {
				failed = &AuthFailedError{Parsed: tt.resp, innerErr: errors.New(tt.resp.ErrorDescription)}
			}
			err, ok := newTokenProtectionBlockedSuggestion(tt.resp, tt.scopes, failed)
			require.Equal(t, tt.want, ok)
			if !tt.want {
				require.Nil(t, err)
				return
			}

			require.NotNil(t, err)

			// Wrapped as ErrorWithSuggestion with the expected scope-aware message and links.
			ews, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
			require.True(t, ok, "expected error to be *internal.ErrorWithSuggestion")
			require.Equal(t, tt.wantMessage, ews.Message)
			require.Equal(t, "Contact your IT administrator or request a policy exception.", ews.Suggestion)
			require.Len(t, ews.Links, 2)
			require.Equal(t, conditionalAccessDocsLink, ews.Links[0].URL)
			require.NotEmpty(t, ews.Links[0].Title)
			require.Equal(t, tokenProtectionFAQLink, ews.Links[1].URL)
			require.NotEmpty(t, ews.Links[1].Title)

			// Inner error is preserved as the originating *AuthFailedError so AAD semantics
			// (error code, description) flow through unmodified.
			inner, ok := errors.AsType[*AuthFailedError](err)
			require.True(t, ok, "expected inner error to be *AuthFailedError")
			require.NotNil(t, inner.Parsed)
			require.Contains(t, inner.Parsed.ErrorCodes, 530084)
		})
	}
}

func TestNewActionableAuthError_TokenProtectionTakesPrecedence(t *testing.T) {
	// AADSTS530084 paired with invalid_grant should produce a token-protection suggestion
	// (not a ReLoginRequiredError), because reauthenticating won't unblock the user.
	resp := &AadErrorResponse{
		Error:            "invalid_grant",
		ErrorDescription: "AADSTS530084: blocked by token protection",
		ErrorCodes:       []int{530084},
	}
	failed := &AuthFailedError{Parsed: resp, innerErr: errors.New(resp.ErrorDescription)}

	err, ok := newActionableAuthError(resp, LoginScopes(cloud.AzurePublic()), cloud.AzurePublic(), "", failed)
	require.True(t, ok)
	require.NotNil(t, err)

	_, isReLogin := errors.AsType[*ReLoginRequiredError](err)
	require.False(t, isReLogin, "should not be classified as ReLoginRequiredError")

	// The wrapper preserves the originating *AuthFailedError (and its AAD code) as the inner err.
	inner, ok := errors.AsType[*AuthFailedError](err)
	require.True(t, ok, "should preserve inner *AuthFailedError")
	require.Contains(t, inner.Parsed.ErrorCodes, 530084)
}

func TestNewTokenProtectionBlockedSuggestion_NilAuthFailedErrorFallsBackToDescription(t *testing.T) {
	resp := &AadErrorResponse{
		Error:            "invalid_grant",
		ErrorDescription: "AADSTS530084: blocked by token protection",
		ErrorCodes:       []int{530084},
	}

	err, ok := newTokenProtectionBlockedSuggestion(resp, LoginScopes(cloud.AzurePublic()), nil)
	require.True(t, ok)

	ews, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
	require.True(t, ok)
	require.Equal(t, resp.ErrorDescription, ews.Unwrap().Error())
}

func TestUsesGraphScope(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		want   bool
	}{
		{"empty", nil, false},
		{"arm_only", []string{"https://management.azure.com/.default"}, false},
		{"graph_default", []string{"https://graph.microsoft.com/.default"}, true},
		{"graph_specific_scope", []string{"https://graph.microsoft.com/User.Read"}, true},
		{"mixed", []string{"https://management.azure.com/.default", "https://graph.microsoft.com/.default"}, true},
		{"graph_substring_not_prefix", []string{"https://example.com/https://graph.microsoft.com/.default"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, usesGraphScope(tt.scopes))
		})
	}
}

func TestAzdCredential_GetToken_GenericError(t *testing.T) {
	pc := &silentErrorClient{
		err: errors.New("network failure"),
	}

	account := public.Account{HomeAccountID: "h1"}
	cred := newAzdCredential(
		pc, &account, cloud.AzurePublic(), "", nil,
	)

	_, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network failure")
}

func TestAzdCredential_GetToken_AuthFailedNotReLogin(t *testing.T) {
	// AuthFailedError that doesn't trigger re-login
	pc := &silentErrorClient{
		err: &AuthFailedError{
			innerErr: errors.New("some auth err"),
		},
	}

	account := public.Account{HomeAccountID: "h1"}
	cred := newAzdCredential(
		pc, &account, cloud.AzurePublic(), "", nil,
	)

	_, err := cred.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)

	var authFailed *AuthFailedError
	require.True(t, errors.As(err, &authFailed))
}

func TestReLoginRequiredError_NonRetriableMarker(t *testing.T) {
	e := &ReLoginRequiredError{errText: "re-login"}
	e.NonRetriable() // just exercise the method
}

func TestAuthFailedError_NonRetriableMarker(t *testing.T) {
	e := &AuthFailedError{innerErr: errors.New("inner")}
	e.NonRetriable() // just exercise the method
}

type errCache struct{}

var errCacheFail = errors.New("cache-write-fail")

func (c *errCache) Set(_ string, _ []byte) error {
	return errCacheFail
}

func TestCredentialForCurrentUser_LoadUserConfigError(t *testing.T) {
	m := &Manager{
		configManager: newMemoryConfigManager(),
		userConfigManager: &failingUserConfigManager{
			err: errors.New("load-fail"),
		},
		cloud: cloud.AzurePublic(),
		credentialCache: &memoryCache{
			cache: map[string][]byte{},
		},
	}

	_, err := m.CredentialForCurrentUser(t.Context(), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetching current user")
}

func TestGetLoggedInSPTenantID_LoadUserConfigError(t *testing.T) {
	m := &Manager{
		configManager: newMemoryConfigManager(),
		userConfigManager: &failingUserConfigManager{
			err: errors.New("load-fail"),
		},
		cloud: cloud.AzurePublic(),
		credentialCache: &memoryCache{
			cache: map[string][]byte{},
		},
	}

	_, err := m.GetLoggedInServicePrincipalTenantID(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetching current user")
}

func TestMode_LoadUserConfigError(t *testing.T) {
	m := &Manager{
		userConfigManager: &failingUserConfigManager{
			err: errors.New("load-fail"),
		},
	}
	_, err := m.Mode()
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetching current user")
}

func TestSetBuiltInAuthMode_ModeError(t *testing.T) {
	m := &Manager{
		userConfigManager: &failingUserConfigManager{
			err: errors.New("load-fail"),
		},
	}
	err := m.SetBuiltInAuthMode()
	require.Error(t, err)
	require.Contains(t, err.Error(), "fetching current auth mode")
}

func TestAzdCredentialGetToken_ClaimsReLoginPath(t *testing.T) {
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	c := cloud.AzurePublic()

	// Build a minimal HTTP response with a Request attached so
	// httpErrorDetails() doesn't panic.
	fakeReq, _ := http.NewRequest(
		http.MethodPost,
		"https://login.microsoftonline.com/common/oauth2/v2.0/token",
		nil)
	resp := &http.Response{
		StatusCode: 401,
		Status:     "401 Unauthorized",
		Request:    fakeReq,
		Body:       http.NoBody,
	}

	// A client that returns an AuthFailedError whose AAD response
	// triggers re-login (interaction_required).
	client := &silentErrorClient{
		err: &AuthFailedError{
			RawResp:  resp,
			innerErr: errors.New("tenant requires MFA"),
			Parsed: &AadErrorResponse{
				Error:            "interaction_required",
				ErrorDescription: "AADSTS50076: need MFA",
			},
		},
	}

	acct := public.Account{HomeAccountID: "home-a"}
	cred := newAzdCredential(client, &acct, c, "", nil)

	opts := tokenRequestOpts(c, "my-claims")
	_, err := cred.GetToken(t.Context(), opts)
	require.Error(t, err)
	// Should be a ReLoginRequiredError.
	var rle *ReLoginRequiredError
	require.True(t, errors.As(err, &rle))
}

type readErrorCache struct{}

var errReadFail = errors.New("read-fail")

func (c *readErrorCache) Read(string) ([]byte, error) {
	return nil, errReadFail
}

func (c *readErrorCache) Set(string, []byte) error { return nil }

type fakeUnmarshaler struct{}

type failingMarshaler struct{}

func TestLoginWithSPCertificate_BadCert(t *testing.T) {
	m := &Manager{
		configManager:     newMemoryConfigManager(),
		userConfigManager: newMemoryUserConfigManager(),
		cloud:             cloud.AzurePublic(),
		credentialCache:   &memoryCache{cache: map[string][]byte{}},
	}

	_, err := m.LoginWithServicePrincipalCertificate(
		t.Context(), "tid", "cid", []byte("not-a-cert"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing certificate")
}

func TestNonRetriable_AuthFailed(t *testing.T) {
	t.Parallel()
	e := &AuthFailedError{innerErr: errWave3Test}
	e.NonRetriable() // should not panic
}

func TestNonRetriable_ReLoginRequired(t *testing.T) {
	t.Parallel()
	e := &ReLoginRequiredError{}
	e.NonRetriable() // should not panic
}
