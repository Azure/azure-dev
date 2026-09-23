// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionListingValidatesEveryPageBeforeReportingAbsence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		status  int
		wantErr bool
	}{
		{name: "empty completed listing", body: `{"value":[]}`},
		{name: "registered later page", body: `{"value":[{"name":"golden","version":"3"}]}`},
		{name: "missing page", status: http.StatusNotFound, wantErr: true},
		{name: "forbidden page", status: http.StatusForbidden, wantErr: true},
		{name: "failed page", status: http.StatusInternalServerError, wantErr: true},
		{name: "empty body", wantErr: true},
		{name: "null envelope", body: "null", wantErr: true},
		{name: "missing value", body: "{}", wantErr: true},
		{name: "null value", body: `{"value":null}`, wantErr: true},
		{name: "non-array value", body: `{"value":{}}`, wantErr: true},
		{name: "invalid JSON", body: "{", wantErr: true},
		{name: "missing version", body: `{"value":[{"name":"golden"}]}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/datasets/golden/versions" {
					_, _ = io.WriteString(w, `{"value":[],"nextLink":"/next"}`)
					return
				}
				assert.Equal(t, "/next", r.URL.Path)
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)
			pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
				&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
			client := NewDatasetClientFromPipeline(srv.URL, pipeline)
			list, err := client.ListDatasetVersions(t.Context(), "golden", testAPIVersion)
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, list)
				assert.False(t, IsNotFound(err), "later-page failure or malformed response is not dataset absence")
			} else {
				require.NoError(t, err)
				require.NotNil(t, list)
				if tc.name == "registered later page" {
					require.Len(t, list.Value, 1)
					assert.Equal(t, "3", list.Value[0].Version)
				} else {
					assert.Empty(t, list.Value)
				}
			}
			assert.Equal(t, []string{"/datasets/golden/versions", "/next"}, requests)
		})
	}
}
