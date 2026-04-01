// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- RemoteCredential ---

func TestRemoteCredential_GetToken_Success(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tokenJSON, _ := json.Marshal(map[string]any{
		"status":    "success",
		"token":     "access-token-123",
		"expiresOn": now.Add(time.Hour).Format(time.RFC3339),
	})

	transport := &fakeTransporter{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(tokenJSON))),
		},
	}

	rc := newRemoteCredential("https://example.com/auth", "api-key", "tenant-1", transport)
	require.NotNil(t, rc)

	tok, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	})
	require.NoError(t, err)
	assert.Equal(t, "access-token-123", tok.Token)
}

func TestRemoteCredential_GetToken_Error(t *testing.T) {
	code := "unauthorized"
	message := "bad credentials"
	tokenJSON, _ := json.Marshal(map[string]any{
		"status":  "error",
		"code":    code,
		"message": message,
	})

	transport := &fakeTransporter{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(
				strings.NewReader(string(tokenJSON)),
			),
		},
	}

	rc := newRemoteCredential(
		"https://example.com/auth", "api-key", "", transport,
	)
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad credentials")
}

func TestRemoteCredential_GetToken_UnexpectedStatus(t *testing.T) {
	tokenJSON, _ := json.Marshal(map[string]any{
		"status": "pending",
	})

	transport := &fakeTransporter{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(tokenJSON))),
		},
	}

	rc := newRemoteCredential("https://example.com/auth", "api-key", "", transport)
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status")
}

func TestRemoteCredential_GetToken_HTTPError(t *testing.T) {
	transport := &fakeTransporter{
		response: &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader("")),
		},
	}

	rc := newRemoteCredential("https://example.com/auth", "api-key", "", transport)
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code")
}

func TestRemoteCredential_GetToken_TransportError(t *testing.T) {
	transport := &fakeTransporter{
		err: context.DeadlineExceeded,
	}

	rc := newRemoteCredential("https://example.com/auth", "api-key", "", transport)
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "making request")
}

func TestRemoteCredential_GetToken_TenantIDOverride(t *testing.T) {
	var capturedBody string
	transport := &fakeTransporter{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"status":"success","token":"t","expiresOn":"2030-01-01T00:00:00Z"}`,
			)),
		},
		onRequest: func(req *http.Request) {
			body, _ := io.ReadAll(req.Body)
			capturedBody = string(body)
		},
	}

	rc := newRemoteCredential("https://example.com/auth", "api-key", "default-tenant", transport)
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes:   []string{"scope1"},
		TenantID: "override-tenant",
	})
	require.NoError(t, err)
	assert.Contains(t, capturedBody, "override-tenant")
}

func TestRemoteCredential_GetToken_InvalidJSON(t *testing.T) {
	transport := &fakeTransporter{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("not json")),
		},
	}

	rc := newRemoteCredential("https://example.com/auth", "api-key", "", transport)
	_, err := rc.GetToken(t.Context(), policy.TokenRequestOptions{
		Scopes: []string{"scope1"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decoding token response")
}

// --- remoteCredentialError ---

func TestRemoteCredentialError(t *testing.T) {
	err := remoteCredentialError("test context", context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "RemoteCredential")
	assert.Contains(t, err.Error(), "test context")
}

// --- fakeTransporter ---

type fakeTransporter struct {
	response  *http.Response
	err       error
	onRequest func(req *http.Request)
}

func (f *fakeTransporter) Do(req *http.Request) (*http.Response, error) {
	if f.onRequest != nil {
		f.onRequest(req)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.response, nil
}
