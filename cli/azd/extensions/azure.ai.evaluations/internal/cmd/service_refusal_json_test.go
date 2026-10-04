// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A named run that does not exist under a verified evaluation is still a
// service refusal, and ReadingRun wraps it rather than replacing it the way
// jobLookupError's not-found branch does. ADO 5572140: the wrapped message
// used to carry the full internal service endpoint into -o json. Existing
// human diagnostics still name which service answered; only the JSON
// projection drops it.
func TestMissingRunKeepsContextButOmitsTheEndpointFromJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("x-ms-error-code", "ResourceNotFound")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"ResourceNotFound","message":"run 'run_missing' was not found"}}`)
	}))
	t.Cleanup(srv.Close)
	ec := evalContextFor(srv)
	cmd := jsonCmd(t, "table")
	cmd.SetContext(t.Context())

	_, _, err := ec.latestOrNamedRun(cmd, "eval_verified", "run_missing", true)
	require.Error(t, err)

	full := err.Error()
	assert.Contains(t, full, "reading run run_missing")
	assert.Contains(t, full, "run 'run_missing' was not found")
	assert.Contains(t, full, "127.0.0.1",
		"existing human diagnostics still name which service answered")

	safe := jsonMessage(err)
	assert.Contains(t, safe, "reading run run_missing",
		"the outer context ReadingRun added must survive")
	assert.Contains(t, safe, "run 'run_missing' was not found",
		"the service's own sentence is still useful and is not what leaked")
	assert.NotContains(t, safe, "127.0.0.1",
		"the JSON document must not disclose the internal service endpoint")

	assert.Equal(t, "ResourceNotFound", errorCode(err),
		"a JSON consumer needs the service's own code when one was supplied")
}

// A terminal generation-job delete conflict goes through jobLookupError's
// other branch, JobActionFailed, which also wraps rather than replaces. ADO
// 5572140. No x-ms-error-code header here, covering the stable http_<status>
// fallback when the service supplies no code of its own.
func TestJobDeleteConflictKeepsContextButOmitsTheEndpointFromJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":{"innererror":{"message":"job still running"}}}`)
	}))
	t.Cleanup(srv.Close)
	ec := evalContextFor(srv)

	removeErr := datasetJobs.remove(t.Context(), ec, "job_1")
	wrapped := jobLookupError("deleting", datasetJobs, "job_1", removeErr)
	require.Error(t, wrapped)

	full := wrapped.Error()
	assert.Contains(t, full, "deleting dataset generation job job_1")
	assert.Contains(t, full, "job still running")
	assert.Contains(t, full, "127.0.0.1",
		"existing human diagnostics still name which service answered")

	safe := jsonMessage(wrapped)
	assert.Contains(t, safe, "deleting dataset generation job job_1")
	assert.Contains(t, safe, "job still running")
	assert.NotContains(t, safe, "127.0.0.1",
		"the JSON document must not disclose the internal service endpoint")

	code := errorCode(wrapped)
	assert.Equal(t, "http_409", code,
		"a stable code must be reported even when the service supplies none")

	// The full -o json failure contract: exactly one document, nonzero exit,
	// the same safe message and code, and no endpoint anywhere in the stream.
	cmd := jsonCmd(t, "json")
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	priorExit := exitProcess
	exitCode := 0
	exitProcess = func(c int) { exitCode = c }
	t.Cleanup(func() { exitProcess = priorExit })

	require.ErrorIs(t, failAs(cmd, wrapped), wrapped)
	assert.Equal(t, 1, exitCode)

	var doc jsonError
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	require.NoError(t, decoder.Decode(&doc))
	require.ErrorIs(t, decoder.Decode(new(any)), io.EOF, "exactly one JSON document")
	assert.Equal(t, safe, doc.Error.Message)
	assert.Equal(t, code, doc.Error.Code)
	assert.NotContains(t, out.String(), "127.0.0.1")
}
