// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"azureaidataset/internal/pkg/dataset_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Both contracts below were pinned only in `tests/cli`, which is behind
// `//go:build live` and so runs nowhere near CI. `ListDatasetVersions` answers
// an unknown name with a 404 error -- `TestAFirstPage404IsStillAbsence` pins
// that -- and both commands took the error path before ever reaching the
// empty-list handling written for them. Nothing in an ordinary build noticed,
// and the comment on the `show` path asserted the opposite of what the service
// does.

// unknownNameClient answers every request the way the service answers a name it
// does not know.
func unknownNameClient(t *testing.T, status int, body string) *dataset_api.DatasetClient {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return dataset_api.NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))
}

// A list is a filter, not a lookup: an unknown name lists nothing and succeeds,
// so `-o json` callers can range over the result and a delete can be checked
// for idempotence by listing what is left.
func TestVersionsListOfAnUnknownNameSucceeds(t *testing.T) {
	client := unknownNameClient(t, http.StatusNotFound, `{"error":{"code":"NotFound"}}`)

	list, err := listVersionsForDisplay(t.Context(), client, "azdcli-no-such-dataset")

	require.NoError(t, err, "an unknown name lists nothing rather than failing")
	require.NotNil(t, list, "the renderer is handed a list, not a nil to guess about")
	assert.Empty(t, list.Value)
}

// A listing that failed for any other reason is still a failure: reporting it as
// "no versions" is how an existing dataset gets restarted at 1.0.
func TestVersionsListSurfacesARefusedListing(t *testing.T) {
	client := unknownNameClient(t, http.StatusForbidden, `{"error":{"code":"AuthorizationFailed"}}`)

	_, err := listVersionsForDisplay(t.Context(), client, "ds")

	require.Error(t, err, "a refused listing is not an empty one")
}

// `show` is the lookup, so it still refuses -- but in a sentence, not by handing
// the reader the HTTP response that produced it.
func TestShowOfAnUnknownNameIsBrief(t *testing.T) {
	client := unknownNameClient(t, http.StatusNotFound, `{"error":{"code":"NotFound"}}`)

	_, err := latestVersionForShow(t.Context(), client, "azdcli-no-such-dataset")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no dataset")
	assert.NotContains(t, err.Error(), "RESPONSE 404",
		"a missing name does not need the whole HTTP body to explain it")
}

// Some deployments answer an unknown name with an empty 200 instead. A dataset
// cannot exist with no versions, so it has to reach the same sentence.
func TestShowTreatsAnEmptyListingAsUnknown(t *testing.T) {
	client := unknownNameClient(t, http.StatusOK, `{"value":[]}`)

	_, err := latestVersionForShow(t.Context(), client, "azdcli-no-such-dataset")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no dataset")
}
