package experimentation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/stretchr/testify/require"
)

func TestParametersAreSent(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	mockEndpoint := "https://test-exp-s2s.msedge.net/ab"
	mockHttp := mockhttp.NewMockHttpUtil()
	mockHttp.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.String() == mockEndpoint
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		// nolint: staticcheck
		require.ElementsMatch(t, request.Header["X-ExP-Parameters"],
			[]string{
				fmt.Sprintf("machineid=%s", resource.MachineId()),
				fmt.Sprintf("azdversion=%s", internal.VersionInfo().Version.String()),
			},
		)

		res := treatmentAssignmentResponse{
			FlightingVersion:  1,
			AssignmentContext: "context:393182",
		}

		jsonBytes, _ := json.Marshal(res)

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}, nil
	})

	mgr, err := NewAssignmentsManager(mockEndpoint, mockHttp)
	require.NoError(t, err)

	// The mock validates that the required parameters are passed parameter
	// to the request. No need for us to validate the return value.
	_, err = mgr.Assignment(context.Background())
	require.NoError(t, err)
}

func TestCache(t *testing.T) {
	mockEndpoint := "https://test-exp-s2s.msedge.net/ab"
	mockHttp := mockhttp.NewMockHttpUtil()
	mockHttp.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.String() == mockEndpoint
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		res := treatmentAssignmentResponse{
			FlightingVersion: 1,
			Configs: []struct {
				ID         string                 `json:"Id"`
				Parameters map[string]interface{} `json:"Parameters"`
			}{
				{
					ID: "config1",
					Parameters: map[string]interface{}{
						"number":  1,
						"string":  "hello",
						"boolean": true,
					},
				},
			},
			AssignmentContext: "context:393182",
		}

		jsonBytes, _ := json.Marshal(res)

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}, nil
	})

	configRoot := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configRoot)

	mgr, err := NewAssignmentsManager(mockEndpoint, mockHttp)
	require.NoError(t, err)

	assignment, err := mgr.Assignment(context.Background())
	require.NoError(t, err)

	require.Equal(t, "context:393182", assignment.AssignmentContext)

	// The response should have been cached, so we should have a single entry in the cache folder
	// under the config root.
	cacheRoot := filepath.Join(configRoot, cCacheDirectoryName)
	cacheEntries, err := os.ReadDir(cacheRoot)
	require.NoError(t, err)
	require.Len(t, cacheEntries, 1)

	// Create another client and validate that that the cached response is returned and no HTTP request is
	// made.
	mockHttp = mockhttp.NewMockHttpUtil()

	mockHttp.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.String() == mockEndpoint
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		t.Error("HTTP request should not have been made")

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusInternalServerError,
			Header:     http.Header{},
		}, nil
	})

	mgr, err = NewAssignmentsManager(mockEndpoint, mockHttp)
	require.NoError(t, err)

	assignment, err = mgr.Assignment(context.Background())
	require.NoError(t, err)
	require.Equal(t, "context:393182", assignment.AssignmentContext)

	// Now, let's expire the cache and validate that a new HTTP request is made.
	var cacheFile assignmentCacheFile
	cacheData, err := os.ReadFile(filepath.Join(cacheRoot, cacheEntries[0].Name()))
	require.NoError(t, err)
	err = json.Unmarshal(cacheData, &cacheFile)
	require.NoError(t, err)
	cacheFile.ExpiresOn = time.Now().UTC().Add(-1 * time.Hour)
	cacheData, err = json.Marshal(cacheFile)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(cacheRoot, cacheEntries[0].Name()), cacheData, os.ModePerm)
	require.NoError(t, err)

	// We'll return a new assigment context from the mock HTTP server, to simulate the
	// user being assigned to a new experiment.
	mockHttp = mockhttp.NewMockHttpUtil()

	mockHttp.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.String() == mockEndpoint
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		res := treatmentAssignmentResponse{
			FlightingVersion: 2,
			Configs: []struct {
				ID         string                 `json:"Id"`
				Parameters map[string]interface{} `json:"Parameters"`
			}{
				{
					ID: "config1",
					Parameters: map[string]interface{}{
						"number":  1,
						"string":  "hello",
						"boolean": true,
					},
				},
			},
			AssignmentContext: "context:393182;anothercontext:393183",
		}

		jsonBytes, _ := json.Marshal(res)

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}, nil
	})

	mgr, err = NewAssignmentsManager(mockEndpoint, mockHttp)
	require.NoError(t, err)

	assignment, err = mgr.Assignment(context.Background())
	require.NoError(t, err)
	require.Equal(t, "context:393182;anothercontext:393183", assignment.AssignmentContext)
}
