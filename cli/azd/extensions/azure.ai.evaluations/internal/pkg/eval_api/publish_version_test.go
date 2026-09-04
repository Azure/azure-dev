// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRecordingClient points a client at a test server, with no credential
// policy in the pipeline.
//
// MaxRetries -1 disables the SDK's retry policy, so a test that answers 5xx on
// purpose does not spend ten seconds being retried.
func newRecordingClient(t *testing.T, handler http.HandlerFunc) *EvalClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewEvalClientFromPipeline(server.URL, runtime.NewPipeline(
		"test", "v1.0.0", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}}))
}

// versionServer answers a version listing and a publish, assigning whatever
// version the caller decides for each attempt.
func versionServer(t *testing.T, existing []string, assign func(attempt int) string) (
	http.HandlerFunc, *atomic.Int32,
) {
	t.Helper()
	var publishes atomic.Int32

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet {
			values := []map[string]any{}
			for _, v := range existing {
				values = append(values, map[string]any{"name": "tone", "version": v})
			}
			if len(existing) == 0 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":"NotFound"}}`))
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"value": values}))
			return
		}

		attempt := int(publishes.Add(1))
		w.WriteHeader(http.StatusCreated)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"name": "tone", "version": assign(attempt),
		}))
	}, &publishes
}

// A name the project has never seen has no version to collide with, so it must
// publish once and return. Waiting there would tax every first publish for a
// hazard that cannot apply.
func TestCreateEvaluatorVersion_FirstPublishDoesNotRetry(t *testing.T) {
	handler, publishes := versionServer(t, nil, func(int) string { return "1" })
	client := newRecordingClient(t, handler)

	started := time.Now()
	created, err := client.CreateEvaluatorVersion(
		context.Background(), "tone", json.RawMessage(`{}`), nil, "2025-11-15-preview")
	require.NoError(t, err)

	assert.Equal(t, "1", created.Version)
	assert.Equal(t, int32(1), publishes.Load(), "a first publish must be issued once")
	assert.Less(t, time.Since(started), versionSettleInterval,
		"a first publish must not wait on a version that cannot exist")
}

// For a few seconds after a publish the service answers the next one with the
// version it just assigned, replacing that version rather than adding one.
// Accepting it would leave every eval bound to the earlier version scoring
// against a definition nobody chose, so the publish is reissued until the
// version advances.
func TestCreateEvaluatorVersion_RetriesUntilTheVersionAdvances(t *testing.T) {
	handler, publishes := versionServer(t, []string{"1"}, func(attempt int) string {
		if attempt < 3 {
			return "1"
		}
		return "2"
	})
	client := newRecordingClient(t, handler)

	created, err := client.CreateEvaluatorVersion(
		context.Background(), "tone", json.RawMessage(`{}`), nil, "2025-11-15-preview")
	require.NoError(t, err)

	assert.Equal(t, "2", created.Version)
	assert.Equal(t, int32(3), publishes.Load(),
		"the publish must be reissued until the service assigns a new version")
}

// A service that never advances must end in an error rather than in a version
// the caller believes is new. Reporting success there is the failure the whole
// guard exists to prevent.
func TestCreateEvaluatorVersion_GivesUpRatherThanReportASharedVersion(t *testing.T) {
	handler, _ := versionServer(t, []string{"4"}, func(int) string { return "4" })
	client := newRecordingClient(t, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.CreateEvaluatorVersion(
		ctx, "tone", json.RawMessage(`{}`), nil, "2025-11-15-preview")
	require.Error(t, err)
}

// The version listing lags a publish: asked immediately after a create it
// answers 404. A guard that trusted it would stand down in exactly the window
// it exists for, which is why the caller supplies the version it has already
// read.
func TestCreateEvaluatorVersion_UsesTheCallersVersionWhenTheListingLags(t *testing.T) {
	handler, publishes := versionServer(t, nil, func(attempt int) string {
		if attempt < 2 {
			return "1"
		}
		return "2"
	})
	client := newRecordingClient(t, handler)

	created, err := client.CreateEvaluatorVersion(
		context.Background(), "tone", json.RawMessage(`{}`), json.RawMessage(`{"version":"1"}`), "2025-11-15-preview")
	require.NoError(t, err)

	assert.Equal(t, "2", created.Version)
	assert.Equal(t, int32(2), publishes.Load(),
		"the version the caller read must be enough to catch the collision")
}

// A version the service does not number cannot be compared, so it is taken at
// face value: refusing it would make an evaluator unpublishable over a
// convention this extension does not own.
func TestParseVersionNumber(t *testing.T) {
	assert.Equal(t, 7, parseVersionNumber("7"))
	assert.Equal(t, 0, parseVersionNumber("v7"))
	assert.Equal(t, 0, parseVersionNumber(""))
}

// The publish is reissued, so the same body has to arrive every time. A
// closure that consumed its body on the first attempt would send an empty one
// on the second and publish an evaluator with no definition.
func TestCreateEvaluatorVersion_ReissuesTheSameBody(t *testing.T) {
	bodies := make(chan string, 4)
	handler, _ := versionServer(t, []string{"1"}, func(attempt int) string {
		if attempt < 2 {
			return "1"
		}
		return "2"
	})
	client := newRecordingClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			buf := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(buf)
			bodies <- string(buf)
		}
		handler(w, r)
	})

	_, err := client.CreateEvaluatorVersion(
		context.Background(), "tone",
		json.RawMessage(`{"definition":{"type":"rubric"}}`), nil, "2025-11-15-preview")
	require.NoError(t, err)
	close(bodies)

	seen := 0
	for body := range bodies {
		seen++
		assert.Contains(t, body, "rubric", fmt.Sprintf("attempt %d sent an empty body", seen))
	}
	assert.Equal(t, 2, seen)
}
