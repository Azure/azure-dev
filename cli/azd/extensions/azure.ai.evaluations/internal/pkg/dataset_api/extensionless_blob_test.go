// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// datasetServing answers the credential call with a SAS pointing at blobPath on
// itself, then serves that path as a blob and refuses container listings the way
// storage does.
func datasetServing(t *testing.T, blobPath string) (*httptest.Server, *int) {
	t.Helper()

	listed := 0
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/credentials"):
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"blobReferenceForConsumption":{"credential":{"sasUri":%q}}}`,
				base+blobPath+"?sv=1&sig=x")

		case r.URL.Query().Get("restype") == "container", r.URL.Query().Get("comp") == "list":
			// Storage answers 409 when the URI names a blob, not a container.
			listed++
			w.WriteHeader(http.StatusConflict)

		default:
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(`{"a":1}`))
		}
	}))
	t.Cleanup(srv.Close)
	base = srv.URL
	return srv, &listed
}

func clientFor(srv *httptest.Server) *DatasetClient {
	return NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))
}

// A blob name does not have to carry an extension.
//
// The URI shape was the only thing deciding blob from container, so a single
// blob stored under an extensionless name skipped the direct read and was sent
// to the container listing -- which answers 409 for a blob, so `dataset
// download` failed for a dataset that reads perfectly well.
func TestAnExtensionlessBlobIsStillRecognised(t *testing.T) {
	t.Parallel()

	srv, listed := datasetServing(t, "/container/rows")
	content, err := clientFor(srv).ListDatasetContent(t.Context(), "golden", "1.0", "2024-01-01")

	require.NoError(t, err, "storage answered; the name should not have decided this")
	require.NotNil(t, content)
	assert.True(t, content.SingleFile, "one blob, not a container")
	assert.Positive(t, *listed, "the listing was tried first, which is what made the fallback necessary")
}

// The ordinary case is unchanged, and still costs no failed listing.
func TestABlobWithAnExtensionIsReadDirectly(t *testing.T) {
	t.Parallel()

	srv, listed := datasetServing(t, "/container/rows.jsonl")
	content, err := clientFor(srv).ListDatasetContent(t.Context(), "golden", "1.0", "2024-01-01")

	require.NoError(t, err)
	require.NotNil(t, content)
	assert.True(t, content.SingleFile)
	assert.Zero(t, *listed, "the extension said blob, so nothing listed it as a container")
}

// The hint still orders the two probes, which is what keeps the container path
// from paying for a failed blob read.
func TestTheBlobHintOrdersTheProbes(t *testing.T) {
	t.Parallel()

	assert.False(t, looksLikeBlobURI("https://acct.blob.core.windows.net/c?sv=1"),
		"no final segment to read as a file name")
	assert.False(t, looksLikeBlobURI("https://acct.blob.core.windows.net/c/rows?sv=1"),
		"extensionless, so the hint says container and the fallback has to cover it")
	assert.True(t, looksLikeBlobURI("https://acct.blob.core.windows.net/c/rows.jsonl?sv=1"),
		"the ordinary case still reads as a blob")
}

// The fallback is a last resort, not a retry loop: something that is neither a
// readable blob nor a listable container still reports the listing failure.
func TestSomethingThatIsNeitherStillReportsTheListingFailure(t *testing.T) {
	t.Parallel()

	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/credentials") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"blobReferenceForConsumption":{"credential":{"sasUri":%q}}}`,
				base+"/container/rows?sv=1&sig=x")
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	base = srv.URL

	_, err := clientFor(srv).ListDatasetContent(t.Context(), "golden", "1.0", "2024-01-01")

	require.Error(t, err, "neither shape answered, so this stays a failure")
}
