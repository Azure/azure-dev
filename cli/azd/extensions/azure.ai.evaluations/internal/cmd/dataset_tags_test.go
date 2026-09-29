// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/dataset_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A filter the CLI cannot read is refused here rather than sent.
//
// A malformed filter the service ignores comes back as everything, and one it
// rejects comes back as nothing; both look like an answer to the question that
// was asked.
func TestParseTagFiltersRefusesWhatIsNotAPair(t *testing.T) {
	for _, given := range []string{"managed_by", "", "=orphan"} {
		_, err := parseTagFilters([]string{given})
		require.Error(t, err, given)
		assert.Contains(t, err.Error(), "key=value")
	}

	got, err := parseTagFilters([]string{"team=support", "stage=regression"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"team": "support", "stage": "regression"}, got)

	// A value carrying = is one value, not a third term.
	got, err = parseTagFilters([]string{"expr=a=b"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"expr": "a=b"}, got)
}

// One key asked for two values matches nothing, so it is said rather than
// silently answered with only the second.
func TestParseTagFiltersRefusesOneKeyTwice(t *testing.T) {
	_, err := parseTagFilters([]string{"team=support", "team=billing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "support")
	assert.Contains(t, err.Error(), "billing")

	// The same pair twice is the same question asked twice.
	_, err = parseTagFilters([]string{"team=support", "team=support"})
	require.NoError(t, err)
}

// Repeats narrow. Answering `--tag team=support --tag stage=regression` with
// the union returns rows the reader excluded on purpose.
func TestTagFiltersCombineWithAnd(t *testing.T) {
	datasets := []dataset_api.Dataset{
		{Name: "both", Tags: map[string]string{"team": "support", "stage": "regression"}},
		{Name: "team-only", Tags: map[string]string{"team": "support"}},
		{Name: "stage-only", Tags: map[string]string{"stage": "regression"}},
		{Name: "untagged"},
	}

	kept := filterByTags(datasets, map[string]string{
		"team": "support", "stage": "regression",
	})
	require.Len(t, kept, 1)
	assert.Equal(t, "both", kept[0].Name)

	assert.Len(t, filterByTags(datasets, map[string]string{"team": "support"}), 2)
	assert.Len(t, filterByTags(datasets, nil), 4, "no filter keeps everything")
}

// The cell has to read the same way twice, so a reader comparing two listings
// is comparing the datasets rather than the map iteration order.
func TestTagSummaryIsOrdered(t *testing.T) {
	tags := map[string]string{"z": "1", "a": "2", "m": "3"}
	assert.Equal(t, "a=2, m=3, z=1", tagSummary(tags))
	assert.Equal(t, tagSummary(tags), tagSummary(tags))
	assert.Equal(t, "-", tagSummary(nil))
}

// A declaration naming two tags must not delete what the service recorded
// about where the dataset came from.
func TestTagsAlreadyAppliedIgnoresTagsTheServiceOwns(t *testing.T) {
	have := map[string]string{
		"data_generation_job_id": "datagen-1",
		"evaluation_level":       "turn",
	}
	assert.True(t, tagsAlreadyApplied(have, map[string]string{"evaluation_level": "turn"}),
		"a declaration satisfied by what is already there needs no write")
	assert.False(t, tagsAlreadyApplied(have, map[string]string{"evaluation_level": "conversation"}))
	assert.False(t, tagsAlreadyApplied(have, map[string]string{"team": "support"}))
}
