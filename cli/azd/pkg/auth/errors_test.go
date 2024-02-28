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

func TestReLoginRequired(t *testing.T) {
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
			_, got := newReLoginRequiredError(tt.resp, LoginScopes(cloud.AzurePublic()), cloud.AzurePublic())
			require.Equal(t, tt.want, got)
		})
	}
}
