// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCredential satisfies the constructor without reaching for a real token.
type fakeCredential struct{}

func (fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// recordedCall is one request the client made, as the service saw it.
type recordedCall struct {
	method     string
	path       string
	rawPath    string
	apiVersion string
}

// recordingDatasetClient answers every request with body and status, recording
// what was asked. Retries are off so a deliberate failure is one call.
func recordingDatasetClient(t *testing.T, status int, body string) (*DatasetClient, *[]recordedCall) {
	t.Helper()
	calls := &[]recordedCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = append(*calls, recordedCall{
			method:     r.Method,
			path:       r.URL.Path,
			rawPath:    r.URL.EscapedPath(),
			apiVersion: r.URL.Query().Get("api-version"),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return NewDatasetClientFromPipeline(srv.URL, pipeline), calls
}

// The paths are the service contract, and a wrong one costs a round trip to
// find out. Each is pinned against the shape the API documents.
func TestDatasetOperationPaths(t *testing.T) {
	cases := []struct {
		name       string
		call       func(c *DatasetClient) error
		wantMethod string
		wantPath   string
	}{
		{
			name:       "list",
			call:       func(c *DatasetClient) error { _, err := c.ListDatasets(t.Context(), testAPIVersion); return err },
			wantMethod: http.MethodGet,
			wantPath:   "/datasets",
		},
		{
			name: "list versions",
			call: func(c *DatasetClient) error {
				_, err := c.ListDatasetVersions(t.Context(), "ds", testAPIVersion)
				return err
			},
			wantMethod: http.MethodGet,
			wantPath:   "/datasets/ds/versions",
		},
		{
			name: "get",
			call: func(c *DatasetClient) error {
				_, err := c.GetDataset(t.Context(), "ds", "1.0", testAPIVersion)
				return err
			},
			wantMethod: http.MethodGet,
			wantPath:   "/datasets/ds/versions/1.0",
		},
		{
			name: "credential",
			call: func(c *DatasetClient) error {
				_, err := c.GetDatasetCredential(t.Context(), "ds", "1.0", testAPIVersion)
				return err
			},
			wantMethod: http.MethodPost,
			wantPath:   "/datasets/ds/versions/1.0/credentials",
		},
		{
			name: "start pending upload",
			call: func(c *DatasetClient) error {
				_, err := c.StartPendingUpload(t.Context(), "ds", "1.0", testAPIVersion)
				return err
			},
			wantMethod: http.MethodPost,
			wantPath:   "/datasets/ds/versions/1.0/startPendingUpload",
		},
		{
			name: "finalize",
			call: func(c *DatasetClient) error {
				_, err := c.FinalizeDatasetVersion(t.Context(), "ds", "1.0", "https://x/y.jsonl", testAPIVersion)
				return err
			},
			wantMethod: http.MethodPut,
			wantPath:   "/datasets/ds/versions/1.0",
		},
		{
			name: "create",
			call: func(c *DatasetClient) error {
				_, err := c.CreateDataset(t.Context(), &CreateDatasetRequest{Name: "ds"}, testAPIVersion)
				return err
			},
			wantMethod: http.MethodPost,
			wantPath:   "/datasets",
		},
		{
			name: "delete",
			call: func(c *DatasetClient) error {
				return c.DeleteDatasetVersion(t.Context(), "ds", "1.0", testAPIVersion)
			},
			wantMethod: http.MethodDelete,
			wantPath:   "/datasets/ds/versions/1.0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, calls := recordingDatasetClient(t, http.StatusOK, `{"name":"ds","version":"1.0","value":[]}`)
			require.NoError(t, tc.call(client))
			require.Len(t, *calls, 1)
			assert.Equal(t, tc.wantMethod, (*calls)[0].method)
			assert.Equal(t, tc.wantPath, (*calls)[0].path)
			assert.Equal(t, testAPIVersion, (*calls)[0].apiVersion,
				"the service rejects a request that names no api-version")
		})
	}
}

// A name is caller-supplied and a version can be anything the author wrote, so
// both are escaped rather than pasted into the path.
func TestDatasetPathsEscapeNameAndVersion(t *testing.T) {
	client, calls := recordingDatasetClient(t, http.StatusOK, `{}`)
	_, err := client.GetDataset(t.Context(), "my dataset/v", "1.0 beta", testAPIVersion)
	require.NoError(t, err)

	require.Len(t, *calls, 1)
	assert.Equal(t, "/datasets/my%20dataset%2Fv/versions/1.0%20beta", (*calls)[0].rawPath,
		"an unescaped slash would address a different resource entirely")
}

// A delete answers 204 with nothing in it, which must not read as a failure to
// parse a body that was never promised.
func TestDeleteDatasetVersionAcceptsNoContent(t *testing.T) {
	client, calls := recordingDatasetClient(t, http.StatusNoContent, "")
	require.NoError(t, client.DeleteDatasetVersion(t.Context(), "ds", "1.0", testAPIVersion))
	assert.Len(t, *calls, 1)
}

// The listing arrives wrapped in a value envelope; reading it flat yields an
// empty list rather than an error, which looks like a project with no datasets.
func TestListDatasetsReadsTheValueEnvelope(t *testing.T) {
	client, _ := recordingDatasetClient(t, http.StatusOK,
		`{"value":[{"name":"a","version":"1.0"},{"name":"b","version":"2.0"}]}`)

	list, err := client.ListDatasets(t.Context(), testAPIVersion)
	require.NoError(t, err)
	require.Len(t, list.Value, 2)
	assert.Equal(t, "a", list.Value[0].Name)
	assert.Equal(t, "2.0", list.Value[1].Version)
}

// A failure has to surface as one, since the caller otherwise proceeds with a
// zero-valued dataset and fails somewhere further away.
func TestDatasetOperationsSurfaceServiceFailures(t *testing.T) {
	client, _ := recordingDatasetClient(t, http.StatusNotFound, `{"error":{"code":"NotFound"}}`)

	_, err := client.GetDataset(t.Context(), "missing", "1.0", testAPIVersion)
	require.Error(t, err)

	err = client.DeleteDatasetVersion(t.Context(), "missing", "1.0", testAPIVersion)
	require.Error(t, err)

	_, err = client.ListDatasets(t.Context(), testAPIVersion)
	require.Error(t, err)
}

// The constructor has to build a usable client — it wires the auth policies
// the live service needs, and nothing else exercises that path.
func TestNewDatasetClient(t *testing.T) {
	client := NewDatasetClient("https://example.services.ai.azure.com/api/projects/p", fakeCredential{})
	require.NotNil(t, client)
	assert.Equal(t, "https://example.services.ai.azure.com/api/projects/p", client.endpoint)
}
