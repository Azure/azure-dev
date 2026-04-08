// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/require"
)

// fakeTransport is a test HTTP transport that returns a canned response.
type fakeTransport struct {
	statusCode int
}

func (f *fakeTransport) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: f.statusCode,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

// newTestClient creates an AgentClient backed by fakeTransport (no auth).
func newTestClient(endpoint string, transport policy.Transporter) *AgentClient {
	pipeline := runtime.NewPipeline(
		"test", "v0.0.0-test",
		runtime.PipelineOptions{},
		&policy.ClientOptions{Transport: transport},
	)
	return &AgentClient{
		endpoint: endpoint,
		pipeline: pipeline,
	}
}

func TestDeleteSession_Accepts200(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{statusCode: http.StatusOK},
	)

	err := client.DeleteSession(
		t.Context(), "my-agent", "sess-1", "", "2025-11-15-preview",
	)
	require.NoError(t, err, "200 OK should be treated as success")
}

func TestDeleteSession_Accepts204(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{statusCode: http.StatusNoContent},
	)

	err := client.DeleteSession(
		t.Context(), "my-agent", "sess-1", "", "2025-11-15-preview",
	)
	require.NoError(t, err, "204 No Content should be treated as success")
}

func TestDeleteSession_Rejects500(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{statusCode: http.StatusInternalServerError},
	)

	err := client.DeleteSession(
		t.Context(), "my-agent", "sess-1", "", "2025-11-15-preview",
	)
	require.Error(t, err, "500 should be an error")
}

func TestGetSession_404ReturnsError(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{statusCode: http.StatusNotFound},
	)

	_, err := client.GetSession(
		t.Context(), "my-agent", "sess-1", "2025-11-15-preview",
	)
	require.Error(t, err, "404 should be an error from GetSession")
}
