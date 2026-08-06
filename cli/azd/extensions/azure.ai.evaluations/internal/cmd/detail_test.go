// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A detail view is two columns with Title Case keys, per the spec's output
// conventions. It is what `show` prints; `-o json` is the machine-readable
// alternative, not the only form.
func TestEmitDetail(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitDetail(&buf, []field{
		{"Name", "support-quality"},
		{"Version", "3"},
		{"Type", "rubric"},
	}))

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	for i, want := range []string{"Name", "Version", "Type"} {
		assert.Truef(t, strings.HasPrefix(lines[i], want),
			"line %d should start with the key %q, got %q", i, want, lines[i])
	}
	assert.Contains(t, lines[0], "support-quality")
}

// A blank value says only that the writer did not know which fields this kind
// has, so it is dropped rather than printed as an empty column.
func TestEmitDetail_DropsEmptyValues(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitDetail(&buf, []field{
		{"Name", "support-quality"},
		{"Description", ""},
		{"Type", "rubric"},
	}))

	assert.NotContains(t, buf.String(), "Description")
	assert.Len(t, strings.Split(strings.TrimRight(buf.String(), "\n"), "\n"), 2)
}

// `evaluator show` used to emit raw JSON whatever was asked for, which left it
// the one `show` with no detail view and nowhere to put a portal link.
func TestRenderEvaluator(t *testing.T) {
	var buf bytes.Buffer
	ec := &evalContext{}

	require.NoError(t, ec.renderEvaluator(context.Background(), &buf, &eval_api.EvaluatorSummary{
		Name:                      "support-quality",
		Version:                   "3",
		EvaluatorType:             "rubric",
		Description:               "Grades politeness and accuracy.",
		Categories:                []string{"quality", "custom"},
		SupportedEvaluationLevels: []string{"turn", "conversation"},
	}))

	out := buf.String()
	for _, want := range []string{
		"Name", "support-quality",
		"Version", "3",
		"Type", "rubric",
		"Description", "Grades politeness and accuracy.",
		"Categories", "quality, custom",
		"Evaluation Levels", "turn, conversation",
	} {
		assert.Contains(t, out, want)
	}

	// The schemas live in -o json: printed here they would bury the few lines a
	// reader came for.
	assert.NotContains(t, out, "data_schema")
	assert.NotContains(t, out, "init_parameters")
}

// Both spellings of the type field are read, because the listing says
// evaluator_type and other payloads say type.
func TestRenderEvaluator_ReadsEitherTypeSpelling(t *testing.T) {
	for _, e := range []*eval_api.EvaluatorSummary{
		{Name: "x", EvaluatorType: "rubric"},
		{Name: "x", TypeAlias: "rubric"},
	} {
		var buf bytes.Buffer
		ec := &evalContext{}
		require.NoError(t, ec.renderEvaluator(context.Background(), &buf, e))
		assert.Contains(t, buf.String(), "rubric")
	}
}

// Without an azd environment there is no project to address, so the view ends
// at its last field rather than at an empty label.
func TestRenderEvaluator_NoPortalLinkWithoutAProject(t *testing.T) {
	var buf bytes.Buffer
	ec := &evalContext{}

	require.NoError(t, ec.renderEvaluator(context.Background(), &buf,
		&eval_api.EvaluatorSummary{Name: "support-quality", Version: "3"}))

	assert.NotContains(t, buf.String(), "Portal:")
}

// The spec gives evaluator URLs their own shape, distinct from datasets and
// runs, so a link built from the wrong one resolves to nothing.
func TestPortalEvaluatorURLShape(t *testing.T) {
	prefix, err := eval_api.NewPortalPrefix(
		"/subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/rg/" +
			"providers/Microsoft.CognitiveServices/accounts/acct/projects/proj")
	require.NoError(t, err)

	assert.True(t, strings.HasSuffix(
		prefix.EvaluatorURL("support-quality", "3"),
		"/build/evaluations/catalog/support-quality/3"))
}
