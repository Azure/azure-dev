// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"io"
	"net/http"
	"strings"
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

// fakeBodyTransport returns a canned status code and JSON body.
type fakeBodyTransport struct {
	statusCode int
	body       string
}

func (f *fakeBodyTransport) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: f.statusCode,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Request:    req,
	}, nil
}

func TestCreateSession_Returns201WithBody(t *testing.T) {
	body := `{
		"agent_session_id": "sess-new",
		"version_indicator": {"type": "version_ref", "agent_version": "3"},
		"status": "running",
		"created_at": 1700000000,
		"last_accessed_at": 1700000100,
		"expires_at": 1700086400
	}`

	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeBodyTransport{
			statusCode: http.StatusCreated,
			body:       body,
		},
	)

	session, err := client.CreateSession(
		t.Context(), "my-agent", "",
		&CreateAgentSessionRequest{
			VersionIndicator: &VersionIndicator{
				Type:         "version_ref",
				AgentVersion: "3",
			},
		},
		"2025-11-15-preview",
	)

	require.NoError(t, err)
	require.Equal(t, "sess-new", session.AgentSessionID)
	require.Equal(t, "3", session.VersionIndicator.AgentVersion)
	require.Equal(t, AgentSessionStatus("running"), session.Status)
}

func TestListSessions_Returns200WithPagination(t *testing.T) {
	body := `{
		"data": [
			{
				"agent_session_id": "sess-1",
				"version_indicator": {"type": "version_ref", "agent_version": "2"},
				"status": "running",
				"created_at": 1700000000,
				"last_accessed_at": 1700000100,
				"expires_at": 1700086400
			}
		],
		"pagination_token": "next-page-abc"
	}`

	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeBodyTransport{
			statusCode: http.StatusOK,
			body:       body,
		},
	)

	result, err := client.ListSessions(
		t.Context(), "my-agent", nil, nil, "2025-11-15-preview",
	)

	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	require.Equal(t, "sess-1", result.Data[0].AgentSessionID)
	require.NotNil(t, result.PaginationToken)
	require.Equal(t, "next-page-abc", *result.PaginationToken)
}
