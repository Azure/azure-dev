// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// publishedEvaluator is the body the reconciler actually sent to the service,
// decoded, plus how many publishes it made.
type publishedEvaluator struct {
	body      map[string]any
	publishes int
}

// reconcilerPublishingTo builds a reconciler whose service reports the
// evaluator as absent -- a first publish -- and records the body of the create
// request rather than any intermediate value.
//
// Recording at the wire is the point. withCatalogMetadata can be exercised in
// isolation and still not be reached: replacing `published` with `body` at the
// call site keeps such a test green and republishes the exact defect this
// fixes, a version that arrives with a blank catalog name and narrower
// compatibility than the one before it.
//
// Reads answer 404 until something is published and resolve afterwards, which
// is also what keeps awaitEvaluatorReadable from spending its full propagation
// budget against a server that would never catch up.
func reconcilerPublishingTo(t *testing.T) (*evalReconciler, *publishedEvaluator) {
	t.Helper()
	seen := &publishedEvaluator{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// assert, not require: this runs on the server's goroutine, where
		// FailNow aborts mid-response and fails whichever test is running.
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			raw, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			var body map[string]any
			assert.NoError(t, json.Unmarshal(raw, &body))
			seen.body = body
			seen.publishes++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"support-quality","version":"2"}`))
			return
		}
		if seen.publishes == 0 {
			// Nothing has been published, which is the first-publish case: no
			// drift check, nothing to compare the authored definition against.
			http.Error(w, `{"error":{"code":"NotFound"}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/versions") {
			_, _ = w.Write([]byte(`{"value":[{"name":"support-quality","version":"2"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"support-quality","version":"2"}`))
	}))
	t.Cleanup(srv.Close)

	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{},
		&policy.ClientOptions{Retry: policy.RetryOptions{MaxRetries: -1}})
	return &evalReconciler{ec: &evalContext{
		evalClient: eval_api.NewEvalClientFromPipeline(srv.URL, pipeline),
	}}, seen
}

// writeEvaluatorFile puts a definition on disk and returns its path.
func writeEvaluatorFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "support-quality.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// authoredRubric is what `generate` writes and a human edits: type, dimensions
// and pass_threshold, and none of the catalog fields.
const authoredRubric = `{
  "type": "rubric",
  "dimensions": [ { "id": "helpfulness", "description": "Did it help?" } ],
  "pass_threshold": 3
}`

// declaredEvaluator records the catalog fields the service returned when the
// evaluator was generated, which is exactly what publishing has to carry
// forward.
func declaredEvaluator() project.EvaluatorDecl {
	return project.EvaluatorDecl{
		Name:                      "support-quality",
		Source:                    "./evaluators/support-quality.json",
		DisplayName:               "Support quality",
		Categories:                []string{"quality", "custom"},
		SupportedEvaluationLevels: []string{"turn", "conversation"},
	}
}

// ADO 5530209: editing a rubric and running `azd up` published a version with
// a blank display name, no categories, and support narrowed from
// [turn, conversation] to [turn] -- so an evaluator valid for conversation
// evals looked incompatible afterwards.
//
// The declaration already recorded these fields and its own comment said they
// were kept "so that publishing a later version keeps them", but nothing put
// them in the request. This asserts the request, not the helper.
func TestEnsureEvaluatorPublishesTheDeclarationsCatalogMetadata(t *testing.T) {
	r, seen := reconcilerPublishingTo(t)

	version, published, err := r.EnsureEvaluator(
		context.Background(), declaredEvaluator(), writeEvaluatorFile(t, authoredRubric),
	)
	require.NoError(t, err)
	require.True(t, published, "a first publish creates a version")
	assert.Equal(t, "2", version)

	require.Equal(t, 1, seen.publishes, "exactly one version was published")
	require.NotNil(t, seen.body, "nothing reached the service")

	assert.Equal(t, "Support quality", seen.body["display_name"],
		"the published version arrived with a blank catalog name")
	assert.ElementsMatch(t, []any{"quality", "custom"}, seen.body["categories"])
	assert.ElementsMatch(t, []any{"turn", "conversation"}, seen.body["supported_evaluation_levels"],
		"narrowing this is the damaging half: it changes what the evaluator can grade")

	// The rubric itself still goes, unchanged, under the key the service reads
	// it from. Metadata is added beside what the author wrote, not instead of
	// it, and not folded into it.
	assert.Equal(t, "support-quality", seen.body["name"])
	definition, ok := seen.body["definition"].(map[string]any)
	require.True(t, ok, "the authored rubric is sent as the definition")
	assert.Equal(t, "rubric", definition["type"])
	assert.Equal(t, float64(3), definition["pass_threshold"])
	assert.Contains(t, definition, "dimensions")
}

// A document that already states its own catalog fields keeps them: the
// declaration fills only what the document does not say, so a deliberate value
// in the file is not overwritten by a stale one in the configuration.
func TestEnsureEvaluatorDoesNotOverrideCatalogFieldsTheDocumentStates(t *testing.T) {
	r, seen := reconcilerPublishingTo(t)

	// A full document rather than a bare rubric: top-level keys survive
	// normalization only on this shape, which is what makes them the
	// document's own statement rather than part of the rubric.
	document := `{
      "display_name": "What the file says",
      "supported_evaluation_levels": ["conversation"],
      "definition": {
        "type": "rubric",
        "dimensions": [ { "id": "helpfulness", "description": "Did it help?" } ]
      }
    }`

	_, _, err := r.EnsureEvaluator(
		context.Background(), declaredEvaluator(), writeEvaluatorFile(t, document),
	)
	require.NoError(t, err)
	require.NotNil(t, seen.body)

	assert.Equal(t, "What the file says", seen.body["display_name"],
		"the document's own value wins over the declaration's")
	assert.ElementsMatch(t, []any{"conversation"}, seen.body["supported_evaluation_levels"])

	// And a field the document does not state is still filled from the
	// declaration, so this is selective rather than a blanket skip.
	assert.ElementsMatch(t, []any{"quality", "custom"}, seen.body["categories"])
}

// A declaration recording no catalog metadata blanks nothing: publishing an
// evaluator that never had a display name must not send an empty one.
func TestEnsureEvaluatorSendsNoCatalogKeysWhenTheDeclarationHasNone(t *testing.T) {
	r, seen := reconcilerPublishingTo(t)

	bare := project.EvaluatorDecl{
		Name:   "support-quality",
		Source: "./evaluators/support-quality.json",
	}
	_, _, err := r.EnsureEvaluator(
		context.Background(), bare, writeEvaluatorFile(t, authoredRubric),
	)
	require.NoError(t, err)
	require.NotNil(t, seen.body)

	assert.NotContains(t, seen.body, "display_name",
		"an absent value is absent, not blank")
	assert.NotContains(t, seen.body, "categories")
	assert.NotContains(t, seen.body, "supported_evaluation_levels")
}
