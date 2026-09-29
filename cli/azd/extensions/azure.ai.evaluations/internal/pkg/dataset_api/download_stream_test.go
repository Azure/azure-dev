// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// streamingBlobServer answers a credential and then writes rows until the
// reader hangs up, reporting how many it managed to send.
func streamingBlobServer(t *testing.T, rows int) (*DatasetClient, <-chan int) {
	t.Helper()
	sent := make(chan int, 1)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/credentials") {
			w.Header().Set("Content-Type", "application/json")
			// assert, not require: this runs on the server's goroutine.
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"blobReferenceForConsumption": map[string]any{
					"credential": map[string]any{"sasUri": srv.URL + "/c/rows.jsonl?sig=secret"},
				},
			}))
			return
		}

		flusher, _ := w.(http.Flusher)
		count := 0
		for range rows {
			if _, err := io.WriteString(w, `{"query":"x"}`+"\n"); err != nil {
				break
			}
			count++
			if flusher != nil {
				flusher.Flush()
			}
		}
		sent <- count
	}))
	t.Cleanup(srv.Close)

	return NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil)), sent
}

// `run --max-samples N` keeps N rows. Reading the blob into memory before
// parsing made that cap bound the parse and nothing else, so the whole
// registered dataset crossed the wire and sat in the extension process to score
// a handful of rows -- a large enough dataset exhausts it.
//
// The body is handed back unread now, and closing it early is what cuts the
// transfer short.
func TestOpenDatasetContentStopsTheTransferWhenTheReaderStops(t *testing.T) {
	const rows = 200_000
	client, sent := streamingBlobServer(t, rows)

	body, err := client.OpenDatasetContent(context.Background(), "ds", "1.0", testAPIVersion)
	require.NoError(t, err)

	scanner := bufio.NewScanner(body)
	for range 2 {
		require.True(t, scanner.Scan(), "the rows the caller asked for still arrive")
	}
	require.NoError(t, body.Close())

	select {
	case count := <-sent:
		assert.Less(t, count, rows,
			"closing early has to stop the transfer, not just the parse")
	case <-time.After(30 * time.Second):
		t.Fatal("the server never stopped writing, so the transfer was never bounded")
	}
}

// The whole-bytes path still reads to the end, because writing the artifact to
// disk needs all of it.
func TestDownloadDatasetContentStillReadsEverything(t *testing.T) {
	const rows = 64
	client, sent := streamingBlobServer(t, rows)

	data, err := client.DownloadDatasetContent(context.Background(), "ds", "1.0", testAPIVersion)
	require.NoError(t, err)

	assert.Equal(t, rows, strings.Count(string(data), "\n"))
	assert.Equal(t, rows, <-sent)
}
