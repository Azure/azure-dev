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

	msal "github.com/AzureAD/microsoft-authentication-library-for-go/apps/errors"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/stretchr/testify/require"
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
			_, got := newActionableAuthError(tt.resp, LoginScopes(cloud.AzurePublic()), cloud.AzurePublic(), "")
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
			err, _ := newActionableAuthError(tt.resp, LoginScopes(cloud.AzurePublic()), cloud.AzurePublic(), "")
			got := err.Error()
			require.Equal(t, tt.want, got)
		})
	}
}

func TestTokenProtectionBlockedError(t *testing.T) {
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
			err, ok := newTokenProtectionBlockedError(tt.resp, tt.scopes)
			require.Equal(t, tt.want, ok)
			if !tt.want {
				require.Nil(t, err)
				return
			}

			require.NotNil(t, err)

			// Underlying error message preserves the AAD error description.
			require.Equal(t, tt.resp.ErrorDescription, err.Error())

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

			// Inner error must be *TokenProtectionBlockedError and marked non-retriable.
			inner, ok := errors.AsType[*TokenProtectionBlockedError](err)
			require.True(t, ok, "expected inner error to be *TokenProtectionBlockedError")
			require.Equal(t, tt.resp.ErrorDescription, inner.Error())
			_, isInteractionErr := errors.AsType[AuthInteractionError](err)
			require.True(t, isInteractionErr,
				"TokenProtectionBlockedError should satisfy AuthInteractionError through the ErrorWithSuggestion wrapper")
			// Calling NonRetriable should not panic — verifies the marker method exists.
			inner.NonRetriable()
		})
	}
}

func TestNewActionableAuthError_TokenProtectionTakesPrecedence(t *testing.T) {
	// AADSTS530084 paired with invalid_grant should produce a TokenProtectionBlockedError
	// (not a ReLoginRequiredError), because reauthenticating won't unblock the user.
	resp := &AadErrorResponse{
		Error:            "invalid_grant",
		ErrorDescription: "AADSTS530084: blocked by token protection",
		ErrorCodes:       []int{530084},
	}

	err, ok := newActionableAuthError(resp, LoginScopes(cloud.AzurePublic()), cloud.AzurePublic(), "")
	require.True(t, ok)
	require.NotNil(t, err)

	_, isReLogin := errors.AsType[*ReLoginRequiredError](err)
	require.False(t, isReLogin, "should not be classified as ReLoginRequiredError")

	_, isTokenProtection := errors.AsType[*TokenProtectionBlockedError](err)
	require.True(t, isTokenProtection, "should be classified as TokenProtectionBlockedError")
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
