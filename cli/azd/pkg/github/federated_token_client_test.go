// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package github

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestTokenForAudience(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "http://localhost/api/token")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "fake-token")

	mockContext := mocks.NewMockContext(context.Background())

	var req http.Request
	mockContext.HttpClient.When(func(request *http.Request) bool {
		req = *request
		return true
	}).Respond(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBufferString(`{ "value": "abc" }`)),
	})

	client := NewFederatedTokenClient(&policy.ClientOptions{
		Transport: mockContext.HttpClient,
	})

	token, err := client.TokenForAudience(context.Background(), "api://AzureADTokenExchange")
	require.NoError(t, err)

	require.Equal(t, "abc", token)
	require.Equal(t, "Bearer fake-token", req.Header.Get("Authorization"))
	require.Equal(t, "http://localhost/api/token&audience=api%3A%2F%2FAzureADTokenExchange", req.URL.String())
}

func TestTokenForAudienceDefault(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "http://localhost/api/token")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "fake-token")

	mockContext := mocks.NewMockContext(context.Background())

	var req http.Request
	mockContext.HttpClient.When(func(request *http.Request) bool {
		req = *request
		return true
	}).Respond(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBufferString(`{ "value": "abc" }`)),
	})

	client := NewFederatedTokenClient(&policy.ClientOptions{
		Transport: mockContext.HttpClient,
	})

	token, err := client.TokenForAudience(context.Background(), "")
	require.NoError(t, err)

	require.Equal(t, "abc", token)
	require.Equal(t, "Bearer fake-token", req.Header.Get("Authorization"))
	require.Equal(t, "http://localhost/api/token", req.URL.String())
}

func TestTokenForAudienceFailure(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "http://localhost/api/token")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "fake-token")

	mockContext := mocks.NewMockContext(context.Background())

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return true
	}).Respond(&http.Response{
		StatusCode: 400,
		Body:       io.NopCloser(bytes.NewBufferString("")),
	})

	client := NewFederatedTokenClient(&policy.ClientOptions{
		Transport: mockContext.HttpClient,
	})

	_, err := client.TokenForAudience(context.Background(), "api://AzureADTokenExchange")
	require.Error(t, err)
}
