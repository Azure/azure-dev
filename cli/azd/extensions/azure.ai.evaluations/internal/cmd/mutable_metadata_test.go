// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedUpdate is the body of an update the reconciler pushed, or nil when it
// pushed nothing.
type recordedUpdate struct {
	body *eval_api.UpdateOpenAIEvalRequest
}

// reconcilerHoldingEval builds a reconciler whose service holds this eval, and
// records any update pushed to it.
func reconcilerHoldingEval(
	t *testing.T,
	held eval_api.OpenAIEval,
) (*evalReconciler, *recordedUpdate) {
	t.Helper()
	seen := &recordedUpdate{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert, not require: this runs on the server's goroutine, and FailNow
		// there aborts mid-response and fails whichever test is running instead.
		if r.Method == http.MethodPost {
			raw, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			var body eval_api.UpdateOpenAIEvalRequest
			assert.NoError(t, json.Unmarshal(raw, &body))
			seen.body = &body
		}
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(held))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalReconciler{ec: &evalContext{
		evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
	}}, seen
}

// A description is excluded from the fingerprint so that editing it does not
// fork the run history. Excluding it from the digest is not the same as
// ignoring it: the edit still has to reach the service, and this reuse path is
// the only place it can.
func TestPushMutableSendsAnEditedDescription(t *testing.T) {
	r, seen := reconcilerHoldingEval(t, eval_api.OpenAIEval{
		ID:       "eval-1",
		Name:     "support-gate",
		Metadata: map[string]string{metaEvalName: "support-gate", metaDescription: "old wording"},
	})

	r.pushMutable(context.Background(), "eval-1", project.Eval{
		Name:        "support-gate",
		Description: "new wording",
	}, &eval_api.OpenAIEval{
		ID:       "eval-1",
		Name:     "support-gate",
		Metadata: map[string]string{metaEvalName: "support-gate", metaDescription: "old wording"},
	})

	require.NotNil(t, seen.body, "an edited description must be pushed")
	assert.Equal(t, "new wording", seen.body.Metadata[metaDescription])
	assert.Equal(t, "support-gate", seen.body.Name, "the name rides along unchanged")
}

// A rename is applied in place rather than forking the history.
func TestPushMutableSendsARename(t *testing.T) {
	held := eval_api.OpenAIEval{ID: "eval-1", Name: "old-name"}
	r, seen := reconcilerHoldingEval(t, held)

	r.pushMutable(context.Background(), "eval-1",
		project.Eval{Name: "new-name"}, &held)

	require.NotNil(t, seen.body)
	assert.Equal(t, "new-name", seen.body.Name)
}

// Every deploy walks this path, so an unchanged declaration must stay silent.
// Pushing regardless would write to the service on every `azd up`.
func TestPushMutableIsSilentWhenNothingChanged(t *testing.T) {
	held := eval_api.OpenAIEval{
		ID:       "eval-1",
		Name:     "support-gate",
		Metadata: map[string]string{metaEvalName: "support-gate", metaDescription: "wording"},
	}
	r, seen := reconcilerHoldingEval(t, held)

	r.pushMutable(context.Background(), "eval-1", project.Eval{
		Name:        "support-gate",
		Description: "wording",
	}, &held)

	assert.Nil(t, seen.body, "an unchanged eval must not be written to")
}

// Deleting the line from the config is an edit like any other.
func TestPushMutableClearsARemovedDescription(t *testing.T) {
	held := eval_api.OpenAIEval{
		ID:       "eval-1",
		Name:     "support-gate",
		Metadata: map[string]string{metaDescription: "wording that was deleted"},
	}
	r, seen := reconcilerHoldingEval(t, held)

	r.pushMutable(context.Background(), "eval-1",
		project.Eval{Name: "support-gate"}, &held)

	require.NotNil(t, seen.body)
	assert.NotContains(t, seen.body.Metadata, metaDescription)
}

// The update replaces metadata rather than merging it, so anything the service
// or another writer put there has to be carried across or it is dropped.
func TestWithDescriptionKeepsMetadataItDoesNotOwn(t *testing.T) {
	held := map[string]string{
		metaEvalName:    "support-gate",
		metaAgent:       "support-agent",
		"service_added": "keep me",
	}

	merged := withDescription(held, "new wording")

	assert.Equal(t, "new wording", merged[metaDescription])
	assert.Equal(t, "keep me", merged["service_added"])
	assert.Equal(t, "support-agent", merged[metaAgent])
	assert.NotContains(t, held, metaDescription, "the held map must not be mutated")
}
