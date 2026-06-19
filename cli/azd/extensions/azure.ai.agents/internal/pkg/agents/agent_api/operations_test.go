// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
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

type captureTransport struct {
	statusCode int
	body       string
	requests   []*http.Request
}

func (f *captureTransport) Do(req *http.Request) (*http.Response, error) {
	f.requests = append(f.requests, req)

	body := f.body
	if body == "" {
		body = "{}"
	}

	return &http.Response{
		StatusCode: f.statusCode,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

type fakeCredential struct{}

func (fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "test-token",
		ExpiresOn: time.Now().Add(time.Hour),
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

func newCaptureClient(statusCode int, body string) (*AgentClient, *captureTransport) {
	transport := &captureTransport{statusCode: statusCode, body: body}
	return newTestClient(
		"https://test.example.com/api/projects/proj",
		transport,
	), transport
}

func requireIsolationHeaders(
	t *testing.T,
	req *http.Request,
	wantUser, wantChat, wantSession string,
) {
	t.Helper()

	if wantUser == "" {
		require.Empty(t, req.Header.Values(AgentUserIsolationKeyHeader))
	} else {
		require.Equal(t, wantUser, req.Header.Get(AgentUserIsolationKeyHeader))
	}

	if wantChat == "" {
		require.Empty(t, req.Header.Values(AgentChatIsolationKeyHeader))
	} else {
		require.Equal(t, wantChat, req.Header.Get(AgentChatIsolationKeyHeader))
	}

	if wantSession == "" {
		require.Empty(t, req.Header.Values(SessionIsolationKeyHeader))
	} else {
		require.Equal(t, wantSession, req.Header.Get(SessionIsolationKeyHeader))
	}
}

func TestSessionRequestOptions_ApplyHeaders(t *testing.T) {
	tests := []struct {
		name        string
		options     *SessionRequestOptions
		wantUser    string
		wantChat    string
		wantSession string
	}{
		{
			name:     "both user and chat set",
			options:  &SessionRequestOptions{UserIsolationKey: "user-1", ChatIsolationKey: "chat-1"},
			wantUser: "user-1",
			wantChat: "chat-1",
		},
		{
			name:     "user key only",
			options:  &SessionRequestOptions{UserIsolationKey: "user-only"},
			wantUser: "user-only",
		},
		{
			name:     "chat key only",
			options:  &SessionRequestOptions{ChatIsolationKey: "chat-only"},
			wantChat: "chat-only",
		},
		{
			name:        "session key with user key",
			options:     &SessionRequestOptions{SessionIsolationKey: "sess-1", UserIsolationKey: "u"},
			wantUser:    "u",
			wantSession: "sess-1",
		},
		{
			name:    "nil options is a no-op",
			options: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			headers := http.Header{}
			headers.Set("Authorization", "Bearer unchanged")

			tt.options.ApplyHeaders(headers)

			require.Equal(t, "Bearer unchanged", headers.Get("Authorization"))
			require.Equal(t, tt.wantUser, headers.Get(AgentUserIsolationKeyHeader))
			require.Equal(t, tt.wantChat, headers.Get(AgentChatIsolationKeyHeader))
			require.Equal(t, tt.wantSession, headers.Get(SessionIsolationKeyHeader))
		})
	}
}

func TestDeleteSession_Accepts200(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{statusCode: http.StatusOK},
	)

	err := client.DeleteSession(
		t.Context(), "my-agent", "sess-1", AgentEndpointAPIVersion, nil,
	)
	require.NoError(t, err, "200 OK should be treated as success")
}

func TestDeleteSession_Accepts204(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{statusCode: http.StatusNoContent},
	)

	err := client.DeleteSession(
		t.Context(), "my-agent", "sess-1", AgentEndpointAPIVersion, nil,
	)
	require.NoError(t, err, "204 No Content should be treated as success")
}

func TestDeleteSession_Rejects500(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{statusCode: http.StatusInternalServerError},
	)

	err := client.DeleteSession(
		t.Context(), "my-agent", "sess-1", AgentEndpointAPIVersion, nil,
	)
	require.Error(t, err, "500 should be an error")
}

func TestGetSession_404ReturnsError(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{statusCode: http.StatusNotFound},
	)

	_, err := client.GetSession(
		t.Context(), "my-agent", "sess-1", AgentEndpointAPIVersion, nil,
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
		t.Context(), "my-agent",
		&CreateAgentSessionRequest{
			VersionIndicator: &VersionIndicator{
				Type:         "version_ref",
				AgentVersion: "3",
			},
		},
		AgentEndpointAPIVersion,
		nil,
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
		t.Context(), "my-agent", nil, nil, AgentEndpointAPIVersion, nil,
	)

	require.NoError(t, err)
	require.Len(t, result.Data, 1)
	require.Equal(t, "sess-1", result.Data[0].AgentSessionID)
	require.NotNil(t, result.PaginationToken)
	require.Equal(t, "next-page-abc", *result.PaginationToken)
}

func TestSessionLifecycleOperations_ApplyIsolationHeaders(t *testing.T) {
	sessionBody := `{
		"agent_session_id": "sess-1",
		"version_indicator": {"type": "version_ref", "agent_version": "3"},
		"status": "running",
		"created_at": 1700000000,
		"last_accessed_at": 1700000100,
		"expires_at": 1700086400
	}`

	tests := []struct {
		name        string
		statusCode  int
		body        string
		call        func(*AgentClient, *SessionRequestOptions) error
		wantSession string
	}{
		{
			name:       "create",
			statusCode: http.StatusCreated,
			body:       sessionBody,
			call: func(client *AgentClient, options *SessionRequestOptions) error {
				_, err := client.CreateSession(
					t.Context(),
					"my-agent",
					&CreateAgentSessionRequest{},
					AgentEndpointAPIVersion,
					options,
				)
				return err
			},
			wantSession: "session-1",
		},
		{
			name:       "get",
			statusCode: http.StatusOK,
			body:       sessionBody,
			call: func(client *AgentClient, options *SessionRequestOptions) error {
				_, err := client.GetSession(
					t.Context(),
					"my-agent",
					"sess-1",
					AgentEndpointAPIVersion,
					options,
				)
				return err
			},
		},
		{
			name:       "delete",
			statusCode: http.StatusNoContent,
			call: func(client *AgentClient, options *SessionRequestOptions) error {
				return client.DeleteSession(
					t.Context(),
					"my-agent",
					"sess-1",
					AgentEndpointAPIVersion,
					options,
				)
			},
			wantSession: "session-1",
		},
		{
			name:       "list",
			statusCode: http.StatusOK,
			body:       `{"data":[]}`,
			call: func(client *AgentClient, options *SessionRequestOptions) error {
				_, err := client.ListSessions(
					t.Context(),
					"my-agent",
					nil,
					nil,
					AgentEndpointAPIVersion,
					options,
				)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, transport := newCaptureClient(tt.statusCode, tt.body)
			options := &SessionRequestOptions{
				SessionIsolationKey: tt.wantSession,
				UserIsolationKey:    "user-1",
				ChatIsolationKey:    "chat-1",
			}

			require.NoError(t, tt.call(client, options))
			require.Len(t, transport.requests, 1)
			require.Equal(t, "HostedAgents=V1Preview", transport.requests[0].Header.Get("Foundry-Features"))
			requireIsolationHeaders(t, transport.requests[0], "user-1", "chat-1", tt.wantSession)
		})
	}
}

func TestSessionFileOperations_ApplyIsolationHeaders(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		call       func(*AgentClient, *SessionRequestOptions) error
	}{
		{
			name:       "upload",
			statusCode: http.StatusOK,
			call: func(client *AgentClient, options *SessionRequestOptions) error {
				return client.UploadSessionFile(
					t.Context(),
					"my-agent",
					"sess-1",
					"/data/input.txt",
					"v1",
					bytes.NewReader([]byte("hello")),
					options,
				)
			},
		},
		{
			name:       "download",
			statusCode: http.StatusOK,
			body:       "hello",
			call: func(client *AgentClient, options *SessionRequestOptions) error {
				body, err := client.DownloadSessionFile(
					t.Context(),
					"my-agent",
					"sess-1",
					"/data/input.txt",
					"v1",
					options,
				)
				if err != nil {
					return err
				}
				return body.Close()
			},
		},
		{
			name:       "list",
			statusCode: http.StatusOK,
			body:       `{"path":"/","entries":[]}`,
			call: func(client *AgentClient, options *SessionRequestOptions) error {
				_, err := client.ListSessionFiles(
					t.Context(),
					"my-agent",
					"sess-1",
					"",
					"v1",
					options,
				)
				return err
			},
		},
		{
			name:       "remove",
			statusCode: http.StatusNoContent,
			call: func(client *AgentClient, options *SessionRequestOptions) error {
				return client.RemoveSessionFile(
					t.Context(),
					"my-agent",
					"sess-1",
					"/data/input.txt",
					false,
					"v1",
					options,
				)
			},
		},
		{
			name:       "mkdir",
			statusCode: http.StatusNoContent,
			call: func(client *AgentClient, options *SessionRequestOptions) error {
				return client.MkdirSessionFile(
					t.Context(),
					"my-agent",
					"sess-1",
					"/data",
					"v1",
					options,
				)
			},
		},
		{
			name:       "stat",
			statusCode: http.StatusOK,
			body:       `{"name":"input.txt","path":"/data/input.txt","is_dir":false}`,
			call: func(client *AgentClient, options *SessionRequestOptions) error {
				_, err := client.StatSessionFile(
					t.Context(),
					"my-agent",
					"sess-1",
					"/data/input.txt",
					"v1",
					options,
				)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, transport := newCaptureClient(tt.statusCode, tt.body)
			options := &SessionRequestOptions{
				UserIsolationKey: "user-1",
				ChatIsolationKey: "chat-1",
			}

			require.NoError(t, tt.call(client, options))
			require.Len(t, transport.requests, 1)
			requireIsolationHeaders(t, transport.requests[0], "user-1", "chat-1", "")
		})
	}
}

func TestGetAgentSessionLogStream_ApplyIsolationHeaders(t *testing.T) {
	reqCh := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reqCh <- r:
		default:
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: log\n\n"))
	}))
	defer server.Close()

	client := &AgentClient{
		endpoint:   server.URL,
		credential: fakeCredential{},
	}

	body, err := client.GetAgentSessionLogStream(
		t.Context(),
		"my-agent",
		"sess-1",
		"v1",
		"console",
		50,
		false,
		&SessionRequestOptions{
			UserIsolationKey: "user-1",
			ChatIsolationKey: "chat-1",
		},
	)
	require.NoError(t, err)
	defer body.Close()

	var request *http.Request
	select {
	case request = <-reqCh:
	default:
	}
	require.NotNil(t, request)
	require.Equal(t, "Bearer test-token", request.Header.Get("Authorization"))
	require.Equal(t, "HostedAgents=V1Preview", request.Header.Get("Foundry-Features"))
	requireIsolationHeaders(t, request, "user-1", "chat-1", "")
}

func TestDeleteAgent_ForceTrue(t *testing.T) {
	body := `{"object": "agent", "name": "my-agent", "deleted": true}`
	client, transport := newCaptureClient(http.StatusOK, body)

	result, err := client.DeleteAgent(t.Context(), "my-agent", "v1", true)
	require.NoError(t, err)
	require.True(t, result.Deleted)
	require.Equal(t, "my-agent", result.Name)

	require.Len(t, transport.requests, 1)
	req := transport.requests[0]
	require.Equal(t, http.MethodDelete, req.Method)
	require.Contains(t, req.URL.String(), "/agents/my-agent")
	require.Contains(t, req.URL.RawQuery, "force=true")
}

func TestDeleteAgent_ForceFalse(t *testing.T) {
	body := `{"object": "agent", "name": "my-agent", "deleted": true}`
	client, transport := newCaptureClient(http.StatusOK, body)

	result, err := client.DeleteAgent(t.Context(), "my-agent", "v1", false)
	require.NoError(t, err)
	require.True(t, result.Deleted)

	require.Len(t, transport.requests, 1)
	req := transport.requests[0]
	require.Contains(t, req.URL.RawQuery, "force=false")
}

func TestDeleteAgent_NotFound(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeBodyTransport{statusCode: http.StatusNotFound, body: `{"error": "not found"}`},
	)

	_, err := client.DeleteAgent(t.Context(), "no-agent", "v1", true)
	require.Error(t, err)
}

func TestPatchAgent_Success(t *testing.T) {
	body := `{
		"object": "agent",
		"id": "my-agent",
		"name": "my-agent",
		"versions": {
			"latest": {
				"object": "agent_version",
				"id": "my-agent:1",
				"name": "my-agent",
				"version": "1",
				"created_at": 1700000000
			}
		}
	}`
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeBodyTransport{
			statusCode: http.StatusOK,
			body:       body,
		},
	)

	req := &PatchAgentRequest{
		AgentEndpoint: &AgentEndpoint{
			Protocols: []AgentEndpointProtocol{
				AgentEndpointProtocolResponses, AgentEndpointProtocolA2A,
			},
		},
		AgentCard: &AgentCard{
			Description: "test agent",
			Skills: []AgentCardSkill{
				{
					ID:          "s1",
					Name:        "greet",
					Description: "greets user",
				},
			},
		},
	}

	result, err := client.PatchAgent(
		t.Context(), "my-agent", req, "v1",
	)
	require.NoError(t, err)
	require.Equal(t, "my-agent", result.Name)
}

// TestPatchAgent_SetsAgentEndpointsFeatureHeader verifies the agent_endpoint
// patch is sent with the AgentEndpoints feature enabled. Without this header the
// service ignores agent_endpoint protocols/authorization_schemes (e.g. the
// BotServiceRbac scheme required by activity-protocol agents).
func TestPatchAgent_SetsAgentEndpointsFeatureHeader(t *testing.T) {
	client, transport := newCaptureClient(http.StatusOK, "{}")

	req := &PatchAgentRequest{
		AgentEndpoint: &AgentEndpoint{
			Protocols: []AgentEndpointProtocol{AgentEndpointProtocolActivity},
			AuthorizationSchemes: []AgentEndpointAuthorizationScheme{
				{Type: AgentEndpointAuthSchemeBotServiceRbac},
			},
		},
	}

	_, err := client.PatchAgent(t.Context(), "my-agent", req, "v1")
	require.NoError(t, err)
	require.Len(t, transport.requests, 1)
	require.Equal(t,
		"HostedAgents=V1Preview,AgentEndpoints=V1Preview",
		transport.requests[0].Header.Get("Foundry-Features"),
	)
}

func TestPatchAgent_400ReturnsError(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{statusCode: http.StatusBadRequest},
	)

	req := &PatchAgentRequest{
		AgentEndpoint: &AgentEndpoint{
			Protocols: []AgentEndpointProtocol{AgentEndpointProtocolA2A},
		},
	}

	_, err := client.PatchAgent(
		t.Context(), "my-agent", req, "v1",
	)
	require.Error(t, err, "400 should be an error")
}

func TestPatchAgent_404ReturnsError(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{statusCode: http.StatusNotFound},
	)

	req := &PatchAgentRequest{}

	_, err := client.PatchAgent(
		t.Context(), "no-such-agent", req, "v1",
	)
	require.Error(t, err, "404 should be an error")
}

func TestPatchAgent_500ReturnsError(t *testing.T) {
	client := newTestClient(
		"https://test.example.com/api/projects/proj",
		&fakeTransport{
			statusCode: http.StatusInternalServerError,
		},
	)

	req := &PatchAgentRequest{}

	_, err := client.PatchAgent(
		t.Context(), "my-agent", req, "v1",
	)
	require.Error(t, err, "500 should be an error")
}

func TestPatchAgent_OmitsNilFields(t *testing.T) {
	// Verify that a PatchAgentRequest with only AgentEndpoint
	// does not serialize agent_card in the JSON body.
	req := &PatchAgentRequest{
		AgentEndpoint: &AgentEndpoint{
			Protocols: []AgentEndpointProtocol{AgentEndpointProtocolResponses},
		},
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	s := string(data)
	require.Contains(t, s, `"agent_endpoint"`)
	require.NotContains(t, s, `"agent_card"`)
	require.NotContains(t, s, `"definition"`)
}

// capturingTransport captures the last HTTP request and returns a canned JSON response.
type capturingTransport struct {
	lastReq    *http.Request
	lastBody   []byte
	statusCode int
	respBody   string
}

func (c *capturingTransport) Do(req *http.Request) (*http.Response, error) {
	c.lastReq = req
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		c.lastBody = body
		_ = req.Body.Close()
	}
	return &http.Response{
		StatusCode: c.statusCode,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(c.respBody)),
		Request:    req,
	}, nil
}

func TestZipDeployRequest_MultipartFormat(t *testing.T) {
	agentResp := `{"name":"test-agent","versions":{"latest":{"version":"1","status":"active"}}}`
	transport := &capturingTransport{statusCode: http.StatusCreated, respBody: agentResp}
	client := newTestClient("https://test.example.com/api/projects/proj", transport)

	desc := "test desc"
	metadata := &CreateAgentVersionRequest{
		Description: &desc,
	}
	zipData := []byte("PK\x03\x04fake-zip-content")
	sha256Hex := "abcdef1234567890"

	_, err := client.zipDeployRequest(
		context.Background(),
		"https://test.example.com/api/projects/proj/agents",
		"test-agent",
		metadata,
		zipData,
		sha256Hex,
	)
	require.NoError(t, err)

	// Verify required headers
	require.Equal(t, "CodeAgents=V1Preview,HostedAgents=V1Preview", transport.lastReq.Header.Get("Foundry-Features"))
	require.Equal(t, sha256Hex, transport.lastReq.Header.Get("x-ms-code-zip-sha256"))
	require.Equal(t, "test-agent", transport.lastReq.Header.Get("x-ms-agent-name"))

	// Verify multipart content type with boundary
	contentType := transport.lastReq.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	require.NotEmpty(t, params["boundary"])

	// Parse multipart body and verify parts
	reader := multipart.NewReader(bytes.NewReader(transport.lastBody), params["boundary"])

	// Part 1: metadata
	part1, err := reader.NextPart()
	require.NoError(t, err)
	require.Equal(t, "metadata", part1.FormName())
	require.Equal(t, "application/json", part1.Header.Get("Content-Type"))
	part1Data, _ := io.ReadAll(part1)
	var parsedMeta map[string]any
	require.NoError(t, json.Unmarshal(part1Data, &parsedMeta))
	require.Equal(t, "test desc", parsedMeta["description"])

	// Part 2: code ZIP
	part2, err := reader.NextPart()
	require.NoError(t, err)
	require.Equal(t, "code", part2.FormName())
	require.Equal(t, "agent.zip", part2.FileName())
	part2Data, _ := io.ReadAll(part2)
	require.Equal(t, zipData, part2Data)
}

func TestZipDeployRequest_NoAgentNameHeader_OnUpdate(t *testing.T) {
	agentResp := `{"name":"test-agent","versions":{"latest":{"version":"2","status":"active"}}}`
	transport := &capturingTransport{statusCode: http.StatusOK, respBody: agentResp}
	client := newTestClient("https://test.example.com/api/projects/proj", transport)

	_, err := client.zipDeployRequest(
		context.Background(),
		"https://test.example.com/api/projects/proj/agents/test-agent",
		"", // empty = update, no x-ms-agent-name header
		&CreateAgentVersionRequest{},
		[]byte("zip"),
		"sha",
	)
	require.NoError(t, err)

	// x-ms-agent-name should NOT be set for updates
	require.Empty(t, transport.lastReq.Header.Get("x-ms-agent-name"))
	// But other required headers should still be present
	require.Equal(t, "CodeAgents=V1Preview,HostedAgents=V1Preview", transport.lastReq.Header.Get("Foundry-Features"))
	require.Equal(t, "sha", transport.lastReq.Header.Get("x-ms-code-zip-sha256"))
}

func TestCreateAgentVersion_SetsHostedAgentsPreviewHeader(t *testing.T) {
	// The Foundry v1 endpoint gates POST /agents/{name}/versions on the
	// HostedAgents=V1Preview opt-in header and returns 403 preview_feature_required
	// without it. Make sure the client always sends the header so callers don't
	// silently regress to the pre-v1 (preview-API-version) behavior.
	versionResp := `{
		"object": "agent.version",
		"id": "test-agent:1",
		"name": "test-agent",
		"version": "1"
	}`
	transport := &capturingTransport{statusCode: http.StatusCreated, respBody: versionResp}
	client := newTestClient("https://test.example.com/api/projects/proj", transport)

	desc := "test desc"
	req := &CreateAgentVersionRequest{Description: &desc}

	_, err := client.CreateAgentVersion(context.Background(), "test-agent", req, "v1")
	require.NoError(t, err)

	require.NotNil(t, transport.lastReq, "expected request to be captured")
	require.Equal(t, http.MethodPost, transport.lastReq.Method)
	require.Equal(
		t,
		"https://test.example.com/api/projects/proj/agents/test-agent/versions",
		transport.lastReq.URL.Scheme+"://"+transport.lastReq.URL.Host+transport.lastReq.URL.Path,
	)
	require.Equal(t, "v1", transport.lastReq.URL.Query().Get("api-version"))
	require.Equal(
		t,
		"HostedAgents=V1Preview",
		transport.lastReq.Header.Get("Foundry-Features"),
		"CreateAgentVersion must opt in to HostedAgents=V1Preview on the v1 endpoint",
	)
}

// ---------------------------------------------------------------------------
// DownloadAgentCode tests
// ---------------------------------------------------------------------------

// downloadTransport captures the request and returns a response with custom headers.
type downloadTransport struct {
	lastReq    *http.Request
	statusCode int
	respBody   string
	respHeader http.Header
}

func (d *downloadTransport) Do(req *http.Request) (*http.Response, error) {
	d.lastReq = req
	header := http.Header{}
	maps.Copy(header, d.respHeader)
	if header.Get("Content-Type") == "" {
		header.Set("Content-Type", "application/zip")
	}
	return &http.Response{
		StatusCode: d.statusCode,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(d.respBody)),
		Request:    req,
	}, nil
}

func TestDownloadAgentCode_BuildsCorrectURL(t *testing.T) {
	transport := &downloadTransport{
		statusCode: http.StatusOK,
		respBody:   "fake-zip-bytes",
		respHeader: http.Header{
			"X-Ms-Code-Zip-Sha256": {"abc123"},
			"X-Ms-Agent-Version":   {"5"},
		},
	}
	client := newTestClient("https://test.example.com/api/projects/proj", transport)

	result, err := client.DownloadAgentCode(context.Background(), "my-agent", "v1", "")
	require.NoError(t, err)
	defer result.Body.Close()

	require.NotNil(t, transport.lastReq)
	require.Equal(t, http.MethodGet, transport.lastReq.Method)
	require.Equal(
		t,
		"https://test.example.com/api/projects/proj/agents/my-agent/code:download",
		transport.lastReq.URL.Scheme+"://"+transport.lastReq.URL.Host+transport.lastReq.URL.Path,
	)
	require.Equal(t, "v1", transport.lastReq.URL.Query().Get("api-version"))
	require.Empty(t, transport.lastReq.URL.Query().Get("agent_version"), "agent_version should not be set when empty")
}

func TestDownloadAgentCode_IncludesVersionParam(t *testing.T) {
	transport := &downloadTransport{
		statusCode: http.StatusOK,
		respBody:   "fake-zip-bytes",
		respHeader: http.Header{},
	}
	client := newTestClient("https://test.example.com/api/projects/proj", transport)

	result, err := client.DownloadAgentCode(context.Background(), "my-agent", "v1", "3")
	require.NoError(t, err)
	defer result.Body.Close()

	require.Equal(t, "3", transport.lastReq.URL.Query().Get("agent_version"))
}

func TestDownloadAgentCode_SetsFeatureHeader(t *testing.T) {
	transport := &downloadTransport{
		statusCode: http.StatusOK,
		respBody:   "fake-zip",
		respHeader: http.Header{},
	}
	client := newTestClient("https://test.example.com/api/projects/proj", transport)

	result, err := client.DownloadAgentCode(context.Background(), "my-agent", "v1", "")
	require.NoError(t, err)
	defer result.Body.Close()

	require.Equal(
		t,
		"CodeAgents=V1Preview,HostedAgents=V1Preview",
		transport.lastReq.Header.Get("Foundry-Features"),
	)
}

func TestDownloadAgentCode_ReturnsResponseHeaders(t *testing.T) {
	transport := &downloadTransport{
		statusCode: http.StatusOK,
		respBody:   "zip-content",
		respHeader: http.Header{
			"X-Ms-Code-Zip-Sha256": {"deadbeef"},
			"X-Ms-Agent-Version":   {"7"},
		},
	}
	client := newTestClient("https://test.example.com/api/projects/proj", transport)

	result, err := client.DownloadAgentCode(context.Background(), "my-agent", "v1", "")
	require.NoError(t, err)
	defer result.Body.Close()

	require.Equal(t, "deadbeef", result.ContentHash)
	require.Equal(t, "7", result.AgentVersion)

	body, err := io.ReadAll(result.Body)
	require.NoError(t, err)
	require.Equal(t, "zip-content", string(body))
}

func TestDownloadAgentCode_ReturnsErrorOnNon200(t *testing.T) {
	transport := &downloadTransport{
		statusCode: http.StatusNotFound,
		respBody:   `{"error":{"code":"not_found","message":"agent not found"}}`,
		respHeader: http.Header{"Content-Type": {"application/json"}},
	}
	client := newTestClient("https://test.example.com/api/projects/proj", transport)

	_, err := client.DownloadAgentCode(context.Background(), "no-such-agent", "v1", "")
	require.Error(t, err)
}
