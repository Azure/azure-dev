// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Omitting both flags is the zero-to-first-eval path the composite exists for,
// so it has to mean both rather than nothing.
func TestSelectedArtifacts(t *testing.T) {
	cases := []struct {
		name                       string
		dataset, evaluator         bool
		wantDataset, wantEvaluator bool
	}{
		{"neither means both", false, false, true, true},
		{"--dataset narrows", true, false, true, false},
		{"--evaluator narrows", false, true, false, true},
		{"both means both", true, true, true, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotDataset, gotEvaluator := selectedArtifacts(c.dataset, c.evaluator)

			assert.Equal(t, c.wantDataset, gotDataset, "dataset")
			assert.Equal(t, c.wantEvaluator, gotEvaluator, "evaluator")
		})
	}
}

// The spec's defaults. Deriving from the target is what lets `generate` take no
// positional argument at all, and the dataset's suffix says which level its
// rows are at so the two levels do not collide on one name.
func TestGeneratedName_DerivesFromTheTarget(t *testing.T) {
	name, err := generatedName("", "support-agent", "dataset", datasetNameSuffix("turn"))
	require.NoError(t, err)
	assert.Equal(t, "support-agent-turn-tests", name)

	name, err = generatedName(
		"", "support-agent", "dataset", datasetNameSuffix("conversation"))
	require.NoError(t, err)
	assert.Equal(t, "support-agent-conversation-tests", name)

	name, err = generatedName("", "support-agent", "evaluator", "evaluator")
	require.NoError(t, err)
	assert.Equal(t, "support-agent-evaluator", name)
}

func TestGeneratedName_ExplicitWins(t *testing.T) {
	name, err := generatedName("golden", "support-agent", "dataset", "turn-tests")

	require.NoError(t, err)
	assert.Equal(t, "golden", name)
}

// With neither there is nothing to name the artifact after, and the refusal has
// to name both flags that would answer it.
func TestGeneratedName_NeedsSomethingToNameItAfter(t *testing.T) {
	_, err := generatedName("", "", "dataset", "turn-tests")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--dataset-name")
	assert.Contains(t, err.Error(), "--target")
}

// A composite that submits two jobs has to build a plan for each.
func TestBuildGeneratePlans_BuildsBothPlans(t *testing.T) {
	plans, err := buildGeneratePlans(generateRequest{
		flags:           &generateFlags{path: t.TempDir(), target: "support-agent"},
		target:          "support-agent",
		dataset:         true,
		evaluator:       true,
		evaluationLevel: "turn",
	})

	require.NoError(t, err)
	require.Len(t, plans, 2)
	assert.Equal(t, generateKindDataset, plans[0].Kind,
		"dataset first, which is the order its progress is replayed in")
	assert.Equal(t, generateKindEvaluator, plans[1].Kind)
	assert.Equal(t, "support-agent-turn-tests", plans[0].Name)
	assert.Equal(t, "turn", plans[0].EvaluationLevel,
		"the level travels with the plan, so the confirmation can say what a row is")
	assert.Equal(t, "support-agent-evaluator", plans[1].Name)
}

// A conversation dataset holds simulation seeds, not finished exchanges, so it
// gets its own name rather than overwriting the turn-level one.
func TestBuildGeneratePlans_ConversationDatasetIsNamedForItsLevel(t *testing.T) {
	plans, err := buildGeneratePlans(generateRequest{
		flags:           &generateFlags{path: t.TempDir(), target: "support-agent"},
		target:          "support-agent",
		dataset:         true,
		evaluationLevel: "conversation",
	})

	require.NoError(t, err)
	require.Len(t, plans, 1)
	assert.Equal(t, "support-agent-conversation-tests", plans[0].Name)
}

// Narrowing builds one plan, so nothing is submitted for the other.
func TestBuildGeneratePlans_NarrowedToOne(t *testing.T) {
	plans, err := buildGeneratePlans(generateRequest{
		flags:   &generateFlags{path: t.TempDir(), target: "support-agent"},
		target:  "support-agent",
		dataset: true,
	})

	require.NoError(t, err)
	require.Len(t, plans, 1)
	assert.Equal(t, generateKindDataset, plans[0].Kind)
}

// The name becomes a filename, so one carrying a separator would write outside
// the directory generation was pointed at, and --force would overwrite it.
func TestGeneratedName_RefusesANameThatWouldLeaveTheDirectory(t *testing.T) {
	escapes := []string{
		"../outside",
		"..\\outside",
		"sub/dir",
		"sub\\dir",
		"..",
		".",
		"C:\\Windows\\System32\\drivers\\etc\\hosts",
		"/etc/passwd",
	}

	for _, name := range escapes {
		t.Run(name, func(t *testing.T) {
			_, err := generatedName(name, "support-agent", "dataset", "turn-tests")

			require.Errorf(t, err, "%q must not be accepted as a file name", name)
			assert.Contains(t, err.Error(), "file name")
		})
	}
}

// The service decides its own character set. Refusing everything it might
// accept would block names that work.
func TestGeneratedName_AllowsOrdinaryNames(t *testing.T) {
	for _, name := range []string{
		"golden",
		"support-agent-turn-tests",
		"support_agent.v2",
		"caf\u00e9-dataset",
		"dataset 2",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := generatedName(name, "support-agent", "dataset", "turn-tests")

			require.NoError(t, err)
			assert.Equal(t, name, got)
		})
	}

}

func TestGeneratedDatasetDefaultFitsLimitWithStableLevelPrefix(t *testing.T) {
	const target = "agent-framework-agent-observability-responses"
	require.Len(t, target, 45)
	var prefixes []string
	for _, level := range []string{"turn", "conversation"} {
		plans, err := buildGeneratePlans(generateRequest{
			flags:  &generateFlags{path: t.TempDir(), target: target},
			target: target, dataset: true, evaluator: true, evaluationLevel: level,
		})
		require.NoError(t, err)
		require.Len(t, plans, 2)
		name := plans[0].Name
		assert.LessOrEqual(t, utf8.RuneCountInString(name), 50)
		suffix := "-" + datasetNameSuffix(level)
		require.True(t, strings.HasSuffix(name, suffix))
		prefixes = append(prefixes, strings.TrimSuffix(name, suffix))
		assert.Equal(t, target+"-evaluator", plans[1].Name)
		assert.Len(t, plans[1].Name, 55, "the dataset limit must not be invented for evaluators")
		other, err := generatedName("", target+"-other", "dataset", datasetNameSuffix(level))
		require.NoError(t, err)
		assert.NotEqual(t, name, other, "shortening must retain distinct target identities")
	}
	assert.Equal(t, prefixes[0], prefixes[1], "one canonical shortened stem independent of evaluation level")
}

func TestGeneratedDatasetExplicitNameIsNeverTruncated(t *testing.T) {
	for _, size := range []int{49, 50, 51} {
		input := strings.Repeat("a", size)
		got, err := generatedName(input, "agent", "dataset", "turn-tests")
		if size > 50 {
			require.ErrorContains(t, err, "--dataset-name")
			assert.ErrorContains(t, err, "50")
			assert.Empty(t, got)
		} else {
			require.NoError(t, err)
			assert.Equal(t, input, got)
		}
	}
	name := strings.Repeat("e", 55)
	got, err := generatedName(name, "agent", "evaluator", "evaluator")
	require.NoError(t, err)
	assert.Equal(t, name, got)
}

func TestGenerateOverlongDatasetNameFailsBeforeWrites(t *testing.T) {
	h := newInitHarness(t, nil)
	before := initFileSnapshot(t, h.dir)
	err := runGenerate(t, "--dataset", "--dataset-name", strings.Repeat("x", 51), "--no-prompt")
	require.ErrorContains(t, err, "--dataset-name")
	assert.ErrorContains(t, err, "50")
	assert.Equal(t, before, initFileSnapshot(t, h.dir))
}
