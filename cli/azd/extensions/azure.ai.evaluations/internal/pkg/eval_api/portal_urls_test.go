// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"strings"
	"testing"

	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProjectID = "/subscriptions/00000000-1111-2222-3333-444444444444/" +
	"resourceGroups/rg-eval/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"

// A portal URL is printed at the end of a run and is the one thing a user
// clicks. It is assembled from parts rather than returned by the service, so
// nothing but a test says whether it lands anywhere.
func TestPortalPrefix_BuildsEveryDocumentedURL(t *testing.T) {
	p, err := NewPortalPrefix(testProjectID)
	require.NoError(t, err)

	// The subscription travels base64url-encoded without padding, so the
	// literal GUID must not appear anywhere in the result.
	const sub = "00000000-1111-2222-3333-444444444444"

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"eval run", p.EvalRunURL("eval_1", "evalrun_1"), "/build/evaluations/eval_1/run/evalrun_1"},
		{"evaluator", p.EvaluatorURL("quality", "3"), "/build/evaluations/catalog/quality/3"},
		{"dataset", p.DatasetURL("regression", "2"), "/build/data/datasets/regression/2"},
		{"optimization", p.OptimizationURL("support", "op_9"), "/build/agents/support/optimization/op_9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, strings.HasPrefix(tt.got, "https://ai.azure.com/nextgen/r/"),
				"got %s", tt.got)
			assert.True(t, strings.HasSuffix(tt.got, tt.want), "got %s", tt.got)
			assert.Contains(t, tt.got, "rg-eval")
			assert.Contains(t, tt.got, "acct")
			assert.Contains(t, tt.got, "proj")
			assert.NotContains(t, tt.got, sub,
				"the subscription is encoded, so its plain GUID must not appear")
		})
	}
}

// The encoding is what the portal decodes on the other end, so it is pinned
// rather than merely exercised.
func TestEncodeSubscriptionForURL(t *testing.T) {
	encoded, err := encodeSubscriptionForURL("00000000-1111-2222-3333-444444444444")

	require.NoError(t, err)
	assert.NotContains(t, encoded, "=", "padding would need escaping inside a URL segment")
	assert.NotContains(t, encoded, "+", "base64url, not standard base64")
	assert.NotContains(t, encoded, "/", "a slash would split the URL segment")
	assert.Equal(t, "AAAAABERIiIzM0RERERERA", encoded)
}

func TestEncodeSubscriptionForURL_RejectsSomethingThatIsNotAGUID(t *testing.T) {
	_, err := encodeSubscriptionForURL("not-a-subscription")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "subscription")
}

// A resource ID that is not a project has no account to name, and guessing
// would produce a URL that resolves to someone else's project.
func TestNewPortalPrefix_RefusesWhatIsNotAProject(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"not a resource id at all", "hello"},
		{"empty", ""},
		{
			name: "an account rather than a project under it",
			id: "/subscriptions/00000000-1111-2222-3333-444444444444/resourceGroups/rg/" +
				"providers/Microsoft.CognitiveServices/accounts/acct",
		},
		{
			name: "a project whose subscription is not a GUID",
			id: "/subscriptions/not-a-guid/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/acct/projects/proj",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewPortalPrefix(tt.id)

			require.Error(t, err)
			assert.Nil(t, p)
		})
	}
}

// The prefix distinguishes built-in evaluators from ones the project owns,
// which is what decides whether a version is published or referenced.
func TestIsBuiltinEvaluator(t *testing.T) {
	assert.True(t, IsBuiltinEvaluator("builtin.task_adherence"))
	assert.False(t, IsBuiltinEvaluator("task_adherence"))
	assert.False(t, IsBuiltinEvaluator("builtin"), "the dot is part of the prefix")
	assert.False(t, IsBuiltinEvaluator("my.builtin.thing"), "the prefix has to lead")
	assert.False(t, IsBuiltinEvaluator(""))
}

func TestSplitEvaluators(t *testing.T) {
	// `- evaluator: builtin.coherence` fills Evaluator and leaves Name empty.
	// Name is the criterion label in results, so a fixture that put the
	// reference there agreed with the bug rather than with any real config.
	generated, builtin := SplitEvaluators(evalcore.EvaluatorList{
		{Evaluator: "builtin.coherence"},
		{Evaluator: "support-quality"},
		{Evaluator: "builtin.task_adherence", Name: "adherence"},
	})

	require.Len(t, generated, 1)
	assert.Equal(t, "support-quality", generated[0].Evaluator)
	require.Len(t, builtin, 2)
	assert.Equal(t, "builtin.coherence", builtin[0].Evaluator)
	assert.Equal(t, "builtin.task_adherence", builtin[1].Evaluator,
		"a criterion label does not stop a built-in being a built-in")
}

// Both halves come back nil rather than empty for an empty input, so a caller
// checking len() reads the same either way.
func TestSplitEvaluators_Empty(t *testing.T) {
	generated, builtin := SplitEvaluators(nil)

	assert.Empty(t, generated)
	assert.Empty(t, builtin)
}

// This decides whether a value is looked up in the service or opened off disk.
// Getting it wrong sends a path to the registry, or a registered name to the
// filesystem, and neither failure names the real problem.
func TestIsDatasetName(t *testing.T) {
	names := []string{
		"support-regression",
		"dataset_v2",
		"name.with.dots",
		"trailing.txt",
	}
	for _, v := range names {
		assert.Truef(t, IsDatasetName(v), "%q is a registered name", v)
	}

	paths := []string{
		"",
		"data.jsonl",
		"data.json",
		"data.csv",
		"DATA.JSONL",
		"./data.jsonl",
		"evals/datasets/x.jsonl",
		`evals\datasets\x.jsonl`,
		"a/b",
	}
	for _, v := range paths {
		assert.Falsef(t, IsDatasetName(v), "%q is a path, not a name", v)
	}
}
