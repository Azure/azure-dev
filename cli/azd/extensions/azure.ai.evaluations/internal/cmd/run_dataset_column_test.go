// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Scenario 3 reads a trend off two rows of `run list`, and the dataset column
// is what makes the comparison honest: two rates over different rows are not
// the same claim.
func TestRunDataset_NameAndVersion(t *testing.T) {
	got := runDataset(map[string]string{
		metaDataset:        "support-agent-regression",
		metaDatasetVersion: "1",
	})

	assert.Equal(t, "support-agent-regression (v1)", got)
}

// A dataset recorded before it was published has a name but no version yet.
func TestRunDataset_NameWithoutVersion(t *testing.T) {
	got := runDataset(map[string]string{metaDataset: "support-golden"})

	assert.Equal(t, "support-golden", got)
}

// Runs started before the extension recorded this show nothing. Falling back to
// the configuration would print today's dataset against a run that scored a
// different one, which is exactly the drift the column exists to reveal.
func TestRunDataset_UnrecordedShowsNothing(t *testing.T) {
	assert.Empty(t, runDataset(nil))
	assert.Empty(t, runDataset(map[string]string{"evaluation_level": "turn"}))
}
