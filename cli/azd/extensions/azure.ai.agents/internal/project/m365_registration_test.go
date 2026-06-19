// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"
)

// fakeTokenCredential is a no-op TokenCredential for tests.
type fakeTokenCredential struct {
	token string
}

func (f fakeTokenCredential) GetToken(
	context.Context, policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	tok := f.token
	if tok == "" {
		tok = "test-token"
	}
	return azcore.AccessToken{Token: tok, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestHTTPResultOK(t *testing.T) {
	t.Parallel()

	require.True(t, httpResult{statusCode: 200}.ok())
	require.True(t, httpResult{statusCode: 201}.ok())
	require.True(t, httpResult{statusCode: 299}.ok())
	require.False(t, httpResult{statusCode: 199}.ok())
	require.False(t, httpResult{statusCode: 300}.ok())
	require.False(t, httpResult{statusCode: 400}.ok())
	require.False(t, httpResult{statusCode: 500}.ok())
}

func TestM365Client_Do_SetsBearerTokenAndParsesResponse(t *testing.T) {
	t.Parallel()

	var gotAuth, gotAccept, gotContentType, gotMethod string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	defer server.Close()

	client := newM365Client(fakeTokenCredential{token: "secret-token"})
	res, err := client.do(
		t.Context(), http.MethodPost, server.URL, graphScope,
		map[string]any{"hello": "world"},
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.statusCode)
	require.Equal(t, `{"id":"abc"}`, res.body)
	require.True(t, res.ok())

	require.Equal(t, "Bearer secret-token", gotAuth)
	require.Equal(t, "application/json", gotAccept)
	require.Equal(t, "application/json", gotContentType)
	require.Equal(t, http.MethodPost, gotMethod)
	require.JSONEq(t, `{"hello":"world"}`, string(gotBody))
}

func TestM365Client_Do_NoBodyOmitsContentType(t *testing.T) {
	t.Parallel()

	var gotContentType string
	hadBody := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		hadBody = len(b) > 0
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newM365Client(fakeTokenCredential{})
	res, err := client.do(t.Context(), http.MethodGet, server.URL, graphScope, nil)
	require.NoError(t, err)
	require.True(t, res.ok())
	require.Empty(t, gotContentType)
	require.False(t, hadBody)
}
