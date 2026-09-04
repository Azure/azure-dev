// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"net/http"
	"strings"
	"testing"

	"azureaieval/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// digestIDKey is what makes a rename find the eval it already deployed: the id
// is recorded against the eval's substance, so a declaration whose name
// changed still resolves. That only works while the key derives from the
// digest the same way it did last deploy — change the format and every
// deployed eval silently loses its recorded id and gets recreated.
func TestDigestIDKey(t *testing.T) {
	const digest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	key := digestIDKey(digest)

	assert.Equal(t, "EVAL_SUBSTANCE_0123456789ABCDEF_ID", key)
	for _, r := range key {
		assert.Truef(t,
			(r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_',
			"%q is not allowed in an environment key", r)
	}
}

// Same substance, same key — that is the whole mechanism.
func TestDigestIDKey_IsStableForTheSameSubstance(t *testing.T) {
	group := project.Eval{
		Name:    "support",
		Dataset: "support-regression",
		Target:  &project.Target{Name: "support-agent"},
	}

	first, err := project.FingerprintGroup(group)
	require.NoError(t, err)

	renamed := group
	renamed.Name = "support-renamed"
	renamed.Description = "reworded"
	second, err := project.FingerprintGroup(renamed)
	require.NoError(t, err)

	assert.Equal(t, digestIDKey(first), digestIDKey(second),
		"a rename must land on the key the first deploy wrote")
}

// Different substance, different key, so a genuinely new eval does not adopt
// an unrelated one's id.
func TestDigestIDKey_DiffersWhenTheSubstanceDoes(t *testing.T) {
	a, err := project.FingerprintGroup(project.Eval{Name: "x", Dataset: "one"})
	require.NoError(t, err)
	b, err := project.FingerprintGroup(project.Eval{Name: "x", Dataset: "two"})
	require.NoError(t, err)

	assert.NotEqual(t, digestIDKey(a), digestIDKey(b))
}

// The version recorded for an artifact comes out of what the service returned,
// falling back to what the caller already knew.
func TestVersionFromRaw(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		fallback string
		want     string
	}{
		{"version in the body wins", `{"version":"7"}`, "3", "7"},
		{"empty version falls back", `{"version":""}`, "3", "3"},
		{"absent version falls back", `{"name":"x"}`, "3", "3"},
		{"unparseable body falls back", `not json`, "3", "3"},
		{"empty body falls back", ``, "3", "3"},
		{"no fallback either", `{}`, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, versionFromRaw([]byte(tt.raw), tt.fallback))
		})
	}
}

// A criterion binds to a dataset column through `{{item.<name>}}`. Reading the
// name wrong is how a run is submitted against a column the dataset does not
// have, which the service rejects without saying which one.
func TestItemColumn(t *testing.T) {
	bound := map[string]string{
		"{{item.query}}":        "query",
		"{{item.ground_truth}}": "ground_truth",
		"{{item.a.b}}":          "a.b",
	}
	for binding, want := range bound {
		got, ok := itemColumn(binding)
		assert.Truef(t, ok, "%q is a binding", binding)
		assert.Equal(t, want, got)
	}

	notBound := []string{
		"",
		"query",
		"{{item.}}",
		"{{ item.query }}",
		"{{item.query",
		"item.query}}",
		"{{response.output}}",
	}
	for _, binding := range notBound {
		got, ok := itemColumn(binding)
		assert.Falsef(t, ok, "%q is not an item binding", binding)
		assert.Empty(t, got)
	}
}

// An eval is named after what it evaluates and what it reads, so two evals over
// the same agent from different sources do not collide.
func TestDefaultEvalName(t *testing.T) {
	assert.Equal(t, "support-agent-trace-eval",
		defaultEvalName("support-agent", initSourceTraces))
	assert.Equal(t, "support-agent-eval",
		defaultEvalName("support-agent", "dataset"))
	assert.Equal(t, "support-agent-eval",
		defaultEvalName("support-agent", ""))

	assert.NotEqual(t,
		defaultEvalName("support-agent", initSourceTraces),
		defaultEvalName("support-agent", "dataset"),
		"the source is in the name so the two do not collide")
}

// The reattach line printed by --no-wait has to name the group the job
// actually belongs to; the two job types share no collection, so the wrong
// group is a command that returns "not found".
func TestJobLookupErrorNamesTheGroup(t *testing.T) {
	for _, kind := range []jobKind{datasetJobs, evaluatorJobs} {
		err := jobLookupError("reading", kind, "job_1", assert.AnError)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "job_1")
		assert.Truef(t, strings.Contains(err.Error(), kind.name),
			"the error must name the %q group so the retry goes to the right one", kind.name)
	}
}

// A failed delete used to report that a read failed, which sends the reader
// looking for a read that never happened.
func TestJobLookupErrorNamesWhatWasAttempted(t *testing.T) {
	deleteErr := jobLookupError("deleting", datasetJobs, "job_1", assert.AnError)
	require.Error(t, deleteErr)
	assert.Contains(t, deleteErr.Error(), "deleting")
	assert.NotContains(t, deleteErr.Error(), "reading")

	cancelErr := jobLookupError("cancelling", datasetJobs, "job_1", assert.AnError)
	require.Error(t, cancelErr)
	assert.Contains(t, cancelErr.Error(), "cancelling")

	// A genuine 404 still points at the sibling group whatever the verb was.
	notFound := jobLookupError("deleting", datasetJobs, "job_1",
		&azcore.ResponseError{StatusCode: http.StatusNotFound})
	require.Error(t, notFound)
	assert.Contains(t, notFound.Error(), jobKindEvaluator)
}
