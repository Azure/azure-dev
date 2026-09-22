// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeBody reads a publish body back as a map so a test asserts on the wire
// shape rather than on Go values.
func decodeBody(t *testing.T, body json.RawMessage) map[string]any {
	t.Helper()
	var doc map[string]any
	require.NoError(t, json.Unmarshal(body, &doc))
	return doc
}

// The rubric file holds only what a human edits, so a version published from it
// alone arrived with a blank catalog name and whatever compatibility the
// service inferred. Losing conversation support is the part that bites: an
// evaluator valid for conversation evals looks incompatible after an ordinary
// rubric edit. ADO 5530209.
func TestWithCatalogMetadata_CarriesTheDeclarationsCatalogFields(t *testing.T) {
	t.Parallel()

	body := json.RawMessage(`{"name":"quality","definition":{"type":"rubric","dimensions":[]}}`)

	published, err := withCatalogMetadata(body, project.EvaluatorDecl{
		Name:                      "quality",
		DisplayName:               "agent-framework-agent-basic-responses-evaluator",
		Categories:                []string{"quality", "agents"},
		SupportedEvaluationLevels: []string{"turn", "conversation"},
	})
	require.NoError(t, err)

	doc := decodeBody(t, published)
	assert.Equal(t, "agent-framework-agent-basic-responses-evaluator", doc["display_name"])
	assert.Equal(t, []any{"quality", "agents"}, doc["categories"])
	assert.Equal(t, []any{"turn", "conversation"}, doc["supported_evaluation_levels"],
		"an evaluator that graded conversations must keep grading them")

	assert.Equal(t, "quality", doc["name"], "the authored document is preserved")
	require.Contains(t, doc, "definition")
}

// A declaration recording nothing must blank nothing: an evaluator whose
// metadata the service owns is left exactly as authored.
func TestWithCatalogMetadata_EmptyDeclarationChangesNothing(t *testing.T) {
	t.Parallel()

	body := json.RawMessage(`{"name":"quality","definition":{"type":"rubric"}}`)

	published, err := withCatalogMetadata(body, project.EvaluatorDecl{Name: "quality"})
	require.NoError(t, err)

	assert.JSONEq(t, string(body), string(published))
	assert.Equal(t, string(body), string(published),
		"a body that gained nothing is returned as it arrived, not re-marshalled")

	doc := decodeBody(t, published)
	assert.NotContains(t, doc, "display_name")
	assert.NotContains(t, doc, "categories")
	assert.NotContains(t, doc, "supported_evaluation_levels")
}

// A document that states its own catalog fields is the author speaking, and the
// declaration must not overwrite it.
func TestWithCatalogMetadata_DoesNotOverwriteWhatTheDocumentStates(t *testing.T) {
	t.Parallel()

	body := json.RawMessage(`{` +
		`"name":"quality",` +
		`"display_name":"stated in the document",` +
		`"supported_evaluation_levels":["turn"],` +
		`"definition":{"type":"rubric"}}`)

	published, err := withCatalogMetadata(body, project.EvaluatorDecl{
		DisplayName:               "stated in the declaration",
		Categories:                []string{"quality"},
		SupportedEvaluationLevels: []string{"turn", "conversation"},
	})
	require.NoError(t, err)

	doc := decodeBody(t, published)
	assert.Equal(t, "stated in the document", doc["display_name"])
	assert.Equal(t, []any{"turn"}, doc["supported_evaluation_levels"])
	assert.Equal(t, []any{"quality"}, doc["categories"],
		"a field the document does not state is still filled in")
}

// Each field is filled independently, so a declaration carrying one of them
// does not have to carry all three.
func TestWithCatalogMetadata_FillsEachFieldIndependently(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		decl    project.EvaluatorDecl
		present []string
		absent  []string
	}{
		{
			name:    "only a display name",
			decl:    project.EvaluatorDecl{DisplayName: "quality"},
			present: []string{"display_name"},
			absent:  []string{"categories", "supported_evaluation_levels"},
		},
		{
			name:    "only categories",
			decl:    project.EvaluatorDecl{Categories: []string{"agents"}},
			present: []string{"categories"},
			absent:  []string{"display_name", "supported_evaluation_levels"},
		},
		{
			name:    "only levels",
			decl:    project.EvaluatorDecl{SupportedEvaluationLevels: []string{"conversation"}},
			present: []string{"supported_evaluation_levels"},
			absent:  []string{"display_name", "categories"},
		},
		{
			name:    "an empty category list is not a value",
			decl:    project.EvaluatorDecl{Categories: []string{}},
			present: nil,
			absent:  []string{"categories"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			published, err := withCatalogMetadata(
				json.RawMessage(`{"name":"q","definition":{}}`), tt.decl)
			require.NoError(t, err)

			doc := decodeBody(t, published)
			for _, key := range tt.present {
				assert.Contains(t, doc, key)
			}
			for _, key := range tt.absent {
				assert.NotContains(t, doc, key)
			}
		})
	}
}

// The merge happens at publish time, after the drift comparison has run. It
// must therefore leave the definition sub-document -- the only thing
// sameDefinition reads -- byte-identical, or every deploy would publish a
// version nobody asked for.
func TestWithCatalogMetadata_LeavesTheComparedDefinitionAlone(t *testing.T) {
	t.Parallel()

	body := json.RawMessage(
		`{"name":"quality","definition":{"type":"rubric","dimensions":[{"id":"a"}],"pass_threshold":0.5}}`)

	published, err := withCatalogMetadata(body, project.EvaluatorDecl{
		DisplayName:               "quality",
		SupportedEvaluationLevels: []string{"turn", "conversation"},
	})
	require.NoError(t, err)

	assert.True(t, sameDefinition(body, published),
		"adding catalog metadata must not read as an edited definition")
}

// A body that is not a JSON object is refused rather than silently published
// without the metadata it was supposed to carry.
func TestWithCatalogMetadata_RefusesABodyItCannotRead(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{`not json`, `null`, `["an","array"]`} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := withCatalogMetadata(json.RawMessage(raw),
				project.EvaluatorDecl{DisplayName: "quality"})
			assert.Error(t, err)
		})
	}
}

// `[]` and `"str"` parse; they are just not objects. Reporting them as
// unparseable sends the author looking for a syntax error that is not there.
func TestWithCatalogMetadata_SeparatesBadSyntaxFromTheWrongShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "an array", raw: `["an","array"]`, want: "not a JSON object"},
		{name: "a bare string", raw: `"just a string"`, want: "not a JSON object"},
		{name: "a number", raw: `7`, want: "not a JSON object"},
		{name: "genuinely unparseable", raw: `{"definition":`, want: "not valid JSON"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := withCatalogMetadata(json.RawMessage(tt.raw),
				project.EvaluatorDecl{DisplayName: "quality"})

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
