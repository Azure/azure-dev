// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// windowedEval is a trace-backed declaration, which is the shape where the
// identity and definition digests differ.
func windowedEval(name string, lookback int) project.Eval {
	return project.Eval{
		Name:       name,
		Source:     &project.SourceDecl{Type: "traces", AgentName: "support-agent", LookbackHours: lookback},
		Evaluators: evalcore.EvaluatorList{{Evaluator: "builtin.relevance"}},
	}
}

// evalServiceReconciler answers every eval read with the given id and records
// what was sent, so EnsureEval can be driven without a service.
func evalServiceReconciler(t *testing.T, env *testEnvServer, existingID string) (*evalReconciler, *[]string) {
	t.Helper()

	var created []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A create posts to the collection; an update posts to one eval, which
		// is how the mutable half of a declaration is pushed.
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/evals") {
			created = append(created, r.URL.Path)
			// assert, not require: this runs on the server's goroutine.
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "eval_new"}))
			return
		}
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": existingID, "name": "whatever"}))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalReconciler{ec: &evalContext{
		azdClient:  newTestAzdClient(t, env),
		envName:    "test",
		evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
	}}, &created
}

// Environments written before the digest was split hold the full digest under
// the recreate key. Reading only the definition made the first deploy after an
// upgrade recreate every eval carrying max_samples or source:, for a file
// nobody had touched, and left the runs taken under it unreachable.
func TestAnEnvironmentFromBeforeTheSplitIsNotReadAsAChange(t *testing.T) {
	group := windowedEval("nightly", 24)

	shipped, err := project.FingerprintGroup(group)
	require.NoError(t, err)
	definition, err := project.FingerprintDefinition(group)
	require.NoError(t, err)
	require.NotEqual(t, shipped, definition,
		"the fixture has to be a declaration where the two digests differ")

	env := &testEnvServer{state: map[string]string{
		// What a build from before the split recorded, plus the id it created.
		project.FingerprintKey("eval", "nightly"): shipped,
		idKey("eval", "nightly"):                  "eval_1",
	}}
	r, created := evalServiceReconciler(t, env, "eval_1")

	id, wasCreated, err := r.EnsureEval(context.Background(), group, "")

	require.NoError(t, err)
	assert.Equal(t, "eval_1", id, "the eval already deployed is the one to keep")
	assert.False(t, wasCreated, "nothing in the file changed")
	assert.Empty(t, *created, "an upgrade must not recreate an eval nobody edited")
	assert.Equal(t, definition, env.stored(t, project.FingerprintKey("eval", "nightly")),
		"and the baseline moves to the definition, so the next deploy compares like with like")
}

// A real edit still recreates, or the escape hatch above would have disabled
// change detection.
func TestAnEditedEvalIsStillRecreated(t *testing.T) {
	group := windowedEval("nightly", 24)
	shipped, err := project.FingerprintGroup(group)
	require.NoError(t, err)

	edited := group
	edited.Evaluators = evalcore.EvaluatorList{{Evaluator: "builtin.coherence"}}

	env := &testEnvServer{state: map[string]string{
		project.FingerprintKey("eval", "nightly"): shipped,
		idKey("eval", "nightly"):                  "eval_1",
	}}
	r, created := evalServiceReconciler(t, env, "eval_1")

	id, wasCreated, err := r.EnsureEval(context.Background(), edited, "")

	require.NoError(t, err)
	assert.Equal(t, "eval_new", id)
	assert.True(t, wasCreated)
	assert.Len(t, *created, 1, "a different evaluator is a different eval")
}

// Substance keys are never removed, so one left by an earlier window edit still
// points at a live eval. A second declaration hashing to it must not adopt it,
// whichever order the file lists the two in -- claiming only as each
// declaration finished made the outcome depend on that order.
func TestADeclarationDoesNotAdoptAnEvalAnotherOneOwns(t *testing.T) {
	owner := windowedEval("nightly", 24)
	ownerDigest, err := project.FingerprintGroup(owner)
	require.NoError(t, err)

	// The newcomer is listed first and hashes to the same substance.
	newcomer := windowedEval("weekly", 24)

	env := &testEnvServer{state: map[string]string{
		digestIDKey(ownerDigest): "eval_1",
		idKey("eval", "nightly"): "eval_1",
	}}
	r, created := evalServiceReconciler(t, env, "eval_1")

	r.ReserveDeclared(context.Background(), []project.Eval{newcomer, owner})
	id, wasCreated, err := r.EnsureEval(context.Background(), newcomer, "")

	require.NoError(t, err)
	assert.True(t, wasCreated, "the second declaration is a second eval")
	assert.NotEqual(t, "eval_1", id, "adopting it would rename the eval nightly owns")
	assert.Len(t, *created, 1)
}

// And a genuine rename still adopts: the old name is gone from the file, so
// nothing reserves the eval it used to be called.
func TestARenamedEvalIsStillAdopted(t *testing.T) {
	group := windowedEval("nightly", 24)
	digest, err := project.FingerprintGroup(group)
	require.NoError(t, err)

	renamed := group
	renamed.Name = "evening"

	env := &testEnvServer{state: map[string]string{
		digestIDKey(digest):      "eval_1",
		idKey("eval", "nightly"): "eval_1",
	}}
	r, created := evalServiceReconciler(t, env, "eval_1")

	r.ReserveDeclared(context.Background(), []project.Eval{renamed})
	id, wasCreated, err := r.EnsureEval(context.Background(), renamed, "")

	require.NoError(t, err)
	assert.Equal(t, "eval_1", id, "a rename keeps the id and the runs under it")
	assert.False(t, wasCreated)
	assert.Empty(t, *created)
}

// A declaration about to be recreated reserves nothing: it is abandoning that
// eval, and holding it back refused the rename that legitimately continues it.
func TestARenameIsStillAdoptedWhenTheOldNameIsRecycled(t *testing.T) {
	original := windowedEval("morning", 24)
	digest, err := project.FingerprintGroup(original)
	require.NoError(t, err)
	shipped, err := project.FingerprintDefinition(original)
	require.NoError(t, err)

	// The same commit renames it and gives the freed name to a different eval.
	renamed := original
	renamed.Name = "evening"
	recycled := windowedEval("morning", 24)
	recycled.Evaluators = evalcore.EvaluatorList{{Evaluator: "builtin.coherence"}}

	env := &testEnvServer{state: map[string]string{
		digestIDKey(digest):                       "eval_1",
		idKey("eval", "morning"):                  "eval_1",
		project.FingerprintKey("eval", "morning"): shipped,
	}}
	r, _ := evalServiceReconciler(t, env, "eval_1")

	r.ReserveDeclared(context.Background(), []project.Eval{recycled, renamed})
	id, wasCreated, err := r.EnsureEval(context.Background(), renamed, "")

	require.NoError(t, err)
	assert.Equal(t, "eval_1", id,
		"the eval morning is abandoning is the one evening continues")
	assert.False(t, wasCreated)
}

// The map is built on first use, because the reconciler is also constructed
// literally in several places and writing to a nil map panics.
func TestClaimingWorksOnAReconcilerBuiltDirectly(t *testing.T) {
	r := &evalReconciler{}

	assert.NotPanics(t, func() { r.claim("eval_1") })
	assert.True(t, r.claimed["eval_1"])
}
