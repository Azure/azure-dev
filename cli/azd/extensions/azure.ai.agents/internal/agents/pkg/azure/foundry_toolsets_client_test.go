// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/require"
)

// roundTripFunc is a test helper that captures HTTP requests.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// newTestPipeline creates an Azure SDK pipeline backed by a custom round-tripper.
func newTestPipeline(fn roundTripFunc) runtime.Pipeline {
	return runtime.NewPipeline(
		"test",
		"v0.0.0",
		runtime.PipelineOptions{},
		&policy.ClientOptions{
			Transport: &http.Client{Transport: fn},
		},
	)
}

// newTestToolboxClient creates a FoundryToolboxClient backed by a custom
// HTTP round-tripper so we can inspect requests and control responses
// without touching the network.
func newTestToolboxClient(
	endpoint string,
	fn roundTripFunc,
) *FoundryToolboxClient {
	return &FoundryToolboxClient{
		endpoint: endpoint,
		pipeline: newTestPipeline(fn),
	}
}

func TestCreateToolboxVersion_URLConstruction(t *testing.T) {
	tests := []struct {
		name        string
		endpoint    string
		toolboxName string
		wantPath    string
		wantQuery   string
	}{
		{
			name:        "simple name",
			endpoint:    "https://example.com",
			toolboxName: "my-toolbox",
			wantPath:    "/toolboxes/my-toolbox/versions",
			wantQuery:   "api-version=" + toolboxesApiVersion,
		},
		{
			name:        "name with special chars is escaped",
			endpoint:    "https://example.com",
			toolboxName: "my toolbox/v2",
			wantPath:    "/toolboxes/my%20toolbox%2Fv2/versions",
			wantQuery:   "api-version=" + toolboxesApiVersion,
		},
		{
			name:        "endpoint with trailing slash",
			endpoint:    "https://example.com/",
			toolboxName: "tools",
			wantPath:    "//toolboxes/tools/versions",
			wantQuery:   "api-version=" + toolboxesApiVersion,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedReq *http.Request

			client := newTestToolboxClient(tt.endpoint, func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"id":"1","name":"tb","version":"v1","tools":[]}`)),
					Header:     make(http.Header),
				}, nil
			})

			_, err := client.CreateToolboxVersion(t.Context(), tt.toolboxName, &CreateToolboxVersionRequest{
				Tools: []map[string]any{},
			})
			require.NoError(t, err)
			require.NotNil(t, capturedReq)

			require.Equal(t, http.MethodPost, capturedReq.Method)
			require.Equal(t, tt.wantPath, capturedReq.URL.EscapedPath())
			require.Equal(t, tt.wantQuery, capturedReq.URL.RawQuery)
		})
	}
}

func TestCreateToolboxVersion_RequiredHeaders(t *testing.T) {
	var capturedReq *http.Request

	client := newTestToolboxClient("https://example.com", func(req *http.Request) (*http.Response, error) {
		capturedReq = req
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"1","name":"tb","version":"v1","tools":[]}`)),
			Header:     make(http.Header),
		}, nil
	})

	_, err := client.CreateToolboxVersion(t.Context(), "test-toolbox", &CreateToolboxVersionRequest{
		Tools: []map[string]any{},
	})
	require.NoError(t, err)
	require.NotNil(t, capturedReq)

	require.Equal(t, toolboxesFeatureHeader, capturedReq.Header.Get("Foundry-Features"))
	require.Equal(t, "application/json", capturedReq.Header.Get("Content-Type"))
}

func TestCreateToolboxVersion_ErrorStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{"200 OK", http.StatusOK, false},
		{"400 Bad Request", http.StatusBadRequest, true},
		{"404 Not Found", http.StatusNotFound, true},
		{"409 Conflict", http.StatusConflict, true},
		{"500 Internal Server Error", http.StatusInternalServerError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestToolboxClient("https://example.com", func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tt.statusCode,
					Body:       io.NopCloser(strings.NewReader(`{"id":"1","name":"tb","version":"v1","tools":[]}`)),
					Header:     make(http.Header),
				}, nil
			})

			_, err := client.CreateToolboxVersion(t.Context(), "test", &CreateToolboxVersionRequest{
				Tools: []map[string]any{},
			})

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGetToolbox_URLConstruction(t *testing.T) {
	tests := []struct {
		name        string
		toolboxName string
		wantPath    string
	}{
		{
			name:        "simple name",
			toolboxName: "my-toolbox",
			wantPath:    "/toolboxes/my-toolbox",
		},
		{
			name:        "name needing escape",
			toolboxName: "test/box",
			wantPath:    "/toolboxes/" + url.PathEscape("test/box"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedReq *http.Request

			client := newTestToolboxClient("https://example.com", func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"id":"1","name":"tb","default_version":"v1"}`)),
					Header:     make(http.Header),
				}, nil
			})

			_, err := client.GetToolbox(t.Context(), tt.toolboxName)
			require.NoError(t, err)
			require.NotNil(t, capturedReq)

			require.Equal(t, http.MethodGet, capturedReq.Method)
			require.Equal(t, tt.wantPath, capturedReq.URL.EscapedPath())
			require.Equal(t, toolboxesFeatureHeader, capturedReq.Header.Get("Foundry-Features"))
		})
	}
}

func TestDeleteToolbox_URLAndStatusCodes(t *testing.T) {
	tests := []struct {
		name        string
		toolboxName string
		statusCode  int
		wantErr     bool
	}{
		{"200 OK", "my-toolbox", http.StatusOK, false},
		{"204 No Content", "my-toolbox", http.StatusNoContent, false},
		{"404 Not Found", "my-toolbox", http.StatusNotFound, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedReq *http.Request

			client := newTestToolboxClient("https://example.com", func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{
					StatusCode: tt.statusCode,
					Body:       io.NopCloser(strings.NewReader("")),
					Header:     make(http.Header),
				}, nil
			})

			err := client.DeleteToolbox(t.Context(), tt.toolboxName)
			require.NotNil(t, capturedReq)
			require.Equal(t, http.MethodDelete, capturedReq.Method)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestToolboxClient_PathEscaping_Adversarial(t *testing.T) {
	tests := []struct {
		name        string
		toolboxName string
	}{
		{"path traversal", "../../../etc/passwd"},
		{"slashes in name", "name/with/slashes"},
		{"backslash traversal", `..\..\evil`},
		{"URL-encoded slash", "..%2F..%2Fevil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedReq *http.Request

			client := newTestToolboxClient("https://example.com", func(req *http.Request) (*http.Response, error) {
				capturedReq = req
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"id":"1","name":"tb","default_version":"v1"}`)),
					Header:     make(http.Header),
				}, nil
			})

			_, _ = client.GetToolbox(t.Context(), tt.toolboxName)
			require.NotNil(t, capturedReq)

			// The escaped name must not introduce extra path segments
			escaped := url.PathEscape(tt.toolboxName)
			expectedPath := fmt.Sprintf("/toolboxes/%s", escaped)
			require.Equal(t, expectedPath, capturedReq.URL.EscapedPath())

			// No raw slashes in the toolbox name segment
			escapedPath := capturedReq.URL.EscapedPath()
			segments := strings.Split(strings.Trim(escapedPath, "/"), "/")
			// Should be exactly: ["toolboxes", "<escaped-name>"]
			// (plus query params handled separately)
			require.GreaterOrEqual(t, len(segments), 2,
				"path should have at least 2 segments")
			require.Equal(t, "toolboxes", segments[0])
		})
	}
}
