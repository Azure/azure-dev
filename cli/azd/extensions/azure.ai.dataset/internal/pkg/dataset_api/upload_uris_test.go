// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uploadServer answers startPendingUpload with the given body and records every
// path it is asked for, so what the upload did can be read back.
func pendingUploadServer(t *testing.T, pendingBody string) (*DatasetClient, *[]string) {
	t.Helper()

	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/startPendingUpload") {
			_, _ = w.Write([]byte(pendingBody))
			return
		}
		// assert, not require: this runs on the server's goroutine.
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"name": "golden", "version": "1"}))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return NewDatasetClientFromPipeline(srv.URL, pipeline), &asked
}

// oneJSONLBody is the dataset these tests publish. UploadVersion takes the rows
// the caller already read, so no file is needed to exercise the upload path.
func oneJSONLBody(t *testing.T) string {
	t.Helper()
	return "{\"query\":\"hi\"}\n"
}

// The SAS says where to write and the blob URI says what to register; they are
// different fields of one response. Only the SAS was checked, so a response
// carrying it without the other uploaded the bytes and then finalized against
// "/golden.jsonl" -- a blob nothing points at, and a publish that failed for a
// reason the message did not name.
func TestAnUploadWithNowhereToRegisterItIsRefusedBeforeTheBlobIsWritten(t *testing.T) {
	client, asked := pendingUploadServer(t,
		`{"blobReference":{"credential":{"sasUri":"https://blob.example.invalid/c?sig=s"}}}`)

	_, err := client.UploadVersion(context.Background(), "golden", "1", oneJSONLBody(t), "2024-01-01")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "blob URI")
	assert.Contains(t, err.Error(), "startPendingUpload")

	joined := strings.Join(*asked, " ")
	assert.Contains(t, joined, "startPendingUpload")
	assert.NotContains(t, joined, "versions/1?",
		"nothing may be finalized when there is nothing to finalize against")
}

// The missing-SAS case keeps its own message, because the two are not the same
// problem and the remedies differ.
func TestAnUploadWithNowhereToWriteKeepsItsOwnMessage(t *testing.T) {
	client, _ := pendingUploadServer(t, `{"blobReference":{"blobUri":"https://blob.example.invalid/c"}}`)

	_, err := client.UploadVersion(context.Background(), "golden", "1", oneJSONLBody(t), "2024-01-01")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload SAS URI")
}
