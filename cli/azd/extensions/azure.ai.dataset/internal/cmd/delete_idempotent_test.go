// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"azureaidataset/internal/pkg/dataset_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runDeleteAgainst drives the delete against a server that answers with one
// status, and hands back what the command printed.
func runDeleteAgainst(t *testing.T, status int, body string, asJSON bool) (string, error) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	client := dataset_api.NewDatasetClientFromPipeline(
		srv.URL, runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, nil))

	cmd := &cobra.Command{Use: "delete"}
	// The same flag set the production code reads, registered the way the azd
	// SDK root supplies it at runtime.
	cmd.Flags().StringP("output", "o", "", "")
	if asJSON {
		require.NoError(t, cmd.Flags().Set("output", outputJSON))
	}
	var out bytes.Buffer
	cmd.SetOut(&out)

	action := &datasetDeleteAction{
		cmd:     cmd,
		name:    "azdcli-never-registered",
		version: "1",
		force:   true,
	}
	err := action.deleteWith(t.Context(), &datasetContext{datasetClient: client})
	return out.String(), err
}

// Deleting a version that is not registered is how a cleanup script ends, so it
// has to succeed. It used to exit nonzero on the service's 404, and the only
// coverage was TestCLIDeleteIsIdempotent -- a suite that is type-checked rather
// than run in CI, so nothing caught it.
func TestDeletingAnAbsentVersionSucceeds(t *testing.T) {
	out, err := runDeleteAgainst(t, http.StatusNotFound,
		`{"error":{"code":"UserError","message":"not found"}}`, false)

	require.NoError(t, err, "an already-absent version is the state the caller asked for")
	assert.Contains(t, out, "nothing to do",
		"the line says the version was already gone rather than claiming a delete")
}

// The JSON document keeps one shape, because a script branches on the state
// reached rather than on which call reached it.
func TestDeletingAnAbsentVersionEmitsTheSameDocument(t *testing.T) {
	out, err := runDeleteAgainst(t, http.StatusNotFound,
		`{"error":{"code":"UserError","message":"not found"}}`, true)
	require.NoError(t, err)

	var doc map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &doc),
		"the delete document has to stay parseable:\n%s", out)
	assert.Equal(t, "deleted", doc["status"])
}

// Anything that is not a 404 still fails, so a permission or service problem is
// not quietly reported as a completed cleanup.
func TestDeletingKeepsEveryOtherFailure(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		_, err := runDeleteAgainst(t, status, `{"error":{"code":"Denied","message":"no"}}`, false)
		assert.Error(t, err, "HTTP %d is not an idempotent no-op", status)
	}
}
