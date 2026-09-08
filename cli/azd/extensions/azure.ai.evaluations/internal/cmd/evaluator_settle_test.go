// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"azureaieval/internal/pkg/eval_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// quickEvaluatorSettle drops the retry delay so a test pins the number of
// attempts rather than the wall clock.
func quickEvaluatorSettle(t *testing.T) {
	t.Helper()
	original := evaluatorListingSettleDelay
	evaluatorListingSettleDelay = time.Millisecond
	t.Cleanup(func() { evaluatorListingSettleDelay = original })
}

// laggingEvaluatorContext answers the version listing with a 404 until it has
// been asked emptyFor times, which is what a listing that has not caught up
// with a just-published evaluator does.
func laggingEvaluatorContext(t *testing.T, emptyFor int32) (*evalContext, *int32) {
	t.Helper()

	var listCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/versions") {
			if atomic.AddInt32(&listCalls, 1) <= emptyFor {
				http.Error(w, `{"error":{"code":"NotFound"}}`, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"value":[{"name":"quality","version":"1"}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"quality","version":"1"}`))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalContext{
		evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
	}, &listCalls
}

// Reading an evaluator without a version is not a point read: the client
// resolves the latest through the version listing, and that listing lags a
// publish. `evaluator create` followed by `evaluator update` -- a first
// authoring session -- therefore hit a 404 and the update was refused for an
// evaluator that plainly existed, with advice to run `create`, which fails in
// turn once the listing catches up.
func TestSettledEvaluatorReadWaitsOutALaggingListing(t *testing.T) {
	quickEvaluatorSettle(t)
	ec, listCalls := laggingEvaluatorContext(t, 2)

	raw, err := settledEvaluatorRead(t.Context(), ec, "quality")

	require.NoError(t, err, "the evaluator is there; the listing was only behind")
	assert.Contains(t, string(raw), "quality")
	assert.Greater(t, atomic.LoadInt32(listCalls), int32(1),
		"the first 404 has to be retried, not believed")
}

// The wait is bounded, and what it decides still refuses. An `update` on a name
// that really does not exist must not quietly become a create.
func TestSettledEvaluatorReadStillReportsAGenuineAbsence(t *testing.T) {
	quickEvaluatorSettle(t)
	ec, listCalls := laggingEvaluatorContext(t, 1000)

	_, err := settledEvaluatorRead(t.Context(), ec, "quality")

	require.Error(t, err)
	assert.True(t, eval_api.IsNotFound(err), "an absence that outlasts the wait is an absence")
	assert.LessOrEqual(t, int(atomic.LoadInt32(listCalls)), evaluatorListingSettleAttempts,
		"the wait is bounded")

	assert.Error(t,
		checkAssetExistence("update", "evaluator", "quality", false, true),
		"and update still refuses it")
}

// Only update pays for the wait. For create an absent evaluator is the expected
// answer, and settling it would put the delay on the common path.
func TestCreateDoesNotPayForTheSettleWait(t *testing.T) {
	assert.NoError(t, checkAssetExistence("create", "evaluator", "quality", false, true),
		"create proceeds on absence without establishing it further")
}
