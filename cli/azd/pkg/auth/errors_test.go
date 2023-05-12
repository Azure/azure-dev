package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	msal "github.com/AzureAD/microsoft-authentication-library-for-go/apps/errors"
	"github.com/stretchr/testify/require"
)

func TestAuthFailedError_Error(t *testing.T) {
	loginCmd := "loginCmd"

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
			name: "Parsed_Error_invalid_grant",
			e: msal.CallErr{
				Resp: respWithBody(marshal(&AadErrorResponse{
					Error: "invalid_grant",
				})),
			},
			want: fmt.Sprintf("reauthentication required, run `%s` to log in", loginCmd),
		},
		{
			name: "Parsed_Error_interaction_required",
			e: msal.CallErr{
				Resp: respWithBody(marshal(&AadErrorResponse{
					Error: "interaction_required",
				})),
			},
			want: fmt.Sprintf("reauthentication required, run `%s` to log in", loginCmd),
		},
		{
			name: "Parsed_Error_UnknownError",
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
		{
			name: "NotParsed_NilResponse",
			e: msal.CallErr{
				Err: errors.New("some error"),
			},
			want: "some error",
		},
		{
			name: "NotParsed_NotMsalCallError",
			e:    errors.New("some error"),
			want: "some error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := newAuthFailedError(tt.e, loginCmd)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}
