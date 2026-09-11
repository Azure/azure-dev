// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The spec's output conventions split the two shapes: a list view is uppercase
// headers over a rule, a detail view is Title Case key/value. `show` returns
// one thing, so it is a detail view -- and all three used to disagree, two
// emitting raw JSON whatever was asked for and one printing a one-row table.
//
// Checked by reading the source rather than running the command, because these
// commands need a service; a shape regression should not wait for a live run.
func TestShowCommandsUseDetailViews(t *testing.T) {
	// Command → the file and function that renders it.
	renderers := map[string]string{
		"dataset show":   "dataset.go",
		"evaluator show": "evaluator.go",
		"show":           "eval_group.go",
	}

	for path, file := range renderers {
		t.Run(path, func(t *testing.T) {
			require.NotNil(t, find(t, path), "the command has to exist to have a shape")

			body, err := os.ReadFile(filepath.Join(".", file))
			require.NoError(t, err)
			assert.Containsf(t, string(body), "emitDetail",
				"%s returns one thing, so %s renders it as a detail view", path, file)
		})
	}
}

// Every command returning data supports -o json, which is what makes the
// detail view a presentation choice rather than a loss of information.
func TestShowCommandsStillAnswerInJSON(t *testing.T) {
	for _, path := range []string{"dataset show", "evaluator show", "show"} {
		cmd := find(t, path)
		// -o comes from the SDK root, so a command must not shadow it.
		assert.Nilf(t, cmd.LocalFlags().Lookup("output"),
			"%s must inherit -o rather than declaring its own", path)
	}
}

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

// The pass mark is what Scenario 4 changes between versions, so a version
// listing that omits it cannot answer the question it is read for.
func TestEvaluatorPassThreshold(t *testing.T) {
	threshold := func(v float64) *eval_api.EvaluatorContract {
		return &eval_api.EvaluatorContract{PassThreshold: &v}
	}

	cases := []struct {
		name string
		in   *eval_api.EvaluatorSummary
		want string
	}{
		{"absent evaluator", nil, ""},
		{"no definition", &eval_api.EvaluatorSummary{}, ""},
		{
			"definition without a threshold",
			&eval_api.EvaluatorSummary{Definition: &eval_api.EvaluatorContract{}},
			"",
		},
		{
			"a threshold of zero is a real threshold, not an absent one",
			&eval_api.EvaluatorSummary{Definition: threshold(0)},
			"0.00",
		},
		{
			"two decimals, because 0.7 and 0.75 pass different samples",
			&eval_api.EvaluatorSummary{Definition: threshold(0.75)},
			"0.75",
		},
		{
			"a trailing zero is kept so the column stays aligned",
			&eval_api.EvaluatorSummary{Definition: threshold(0.8)},
			"0.80",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, evaluatorPassThreshold(tc.in))
		})
	}
}

func TestRenderEvaluatorVersionsShowsTheThreshold(t *testing.T) {
	raised, held := 0.80, 0.70
	var buf bytes.Buffer

	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	require.NoError(t, renderEvaluatorVersions(cmd, &eval_api.EvaluatorListResponse{
		Value: []eval_api.EvaluatorSummary{
			{
				Version:     "3",
				CreatedAt:   "2026-08-03T11:22:04Z",
				Description: "Raised threshold, split cites_policy",
				Definition:  &eval_api.EvaluatorContract{PassThreshold: &raised},
			},
			{
				Version:     "2",
				CreatedAt:   "2026-08-01T14:07:33Z",
				Description: "Tightened offers_next_step criteria",
				Definition:  &eval_api.EvaluatorContract{PassThreshold: &held},
			},
		},
	}))

	out := buf.String()
	for _, want := range []string{
		"VERSION", "CREATED AT", "PASS THRESHOLD", "DESCRIPTION",
		"0.80", "0.70",
		"Raised threshold, split cites_policy",
	} {
		assert.Contains(t, out, want)
	}

	// Name and type are constant down the listing, so printing them would cost
	// width and say nothing.
	assert.NotContains(t, out, "NAME")
}
