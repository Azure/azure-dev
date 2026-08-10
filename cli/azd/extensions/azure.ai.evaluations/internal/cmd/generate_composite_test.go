// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

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
// positional argument at all.
func TestGeneratedName_DerivesFromTheTarget(t *testing.T) {
	name, err := generatedName("", "support-agent", "dataset")
	require.NoError(t, err)
	assert.Equal(t, "support-agent-dataset", name)

	name, err = generatedName("", "support-agent", "evaluator")
	require.NoError(t, err)
	assert.Equal(t, "support-agent-evaluator", name)
}

func TestGeneratedName_ExplicitWins(t *testing.T) {
	name, err := generatedName("golden", "support-agent", "dataset")

	require.NoError(t, err)
	assert.Equal(t, "golden", name)
}

// With neither there is nothing to name the artifact after, and the refusal has
// to name both flags that would answer it.
func TestGeneratedName_NeedsSomethingToNameItAfter(t *testing.T) {
	_, err := generatedName("", "", "dataset")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--dataset-name")
	assert.Contains(t, err.Error(), "--target")
}

// A composite that submits two jobs has to build a plan for each.
func TestBuildGeneratePlans_BuildsBothPlans(t *testing.T) {
	plans, err := buildGeneratePlans(generateRequest{
		flags:     &generateFlags{path: t.TempDir(), target: "support-agent"},
		target:    "support-agent",
		dataset:   true,
		evaluator: true,
	})

	require.NoError(t, err)
	require.Len(t, plans, 2)
	assert.Equal(t, generateKindDataset, plans[0].Kind,
		"dataset first, which is the order its progress is replayed in")
	assert.Equal(t, generateKindEvaluator, plans[1].Kind)
	assert.Equal(t, "support-agent-dataset", plans[0].Name)
	assert.Equal(t, "support-agent-evaluator", plans[1].Name)
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
			_, err := generatedName(name, "support-agent", "dataset")

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
		"support-agent-dataset",
		"support_agent.v2",
		"caf\u00e9-dataset",
		"dataset 2",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := generatedName(name, "support-agent", "dataset")

			require.NoError(t, err)
			assert.Equal(t, name, got)
		})
	}
}
