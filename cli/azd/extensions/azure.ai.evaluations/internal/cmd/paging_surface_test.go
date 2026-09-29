// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every listing whose service answers with a cursor offers --after, so a reader
// who learns paging on one command has learned it on all of them.
//
// `job list` had --limit and --all but no --after, which read as "this listing
// cannot be resumed". Its service answers with has_more and last_id like the
// others, so the flag was missing rather than inapplicable, and a caller past
// the first page had no way to ask for the rest.
func TestEveryResumableListingOffersTheCursor(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"list",
		"run list",
		"job list",
	} {
		cmd := find(t, path)

		for _, flag := range []string{"limit", "after", "all"} {
			assert.NotNil(t, cmd.Flags().Lookup(flag),
				"%s should offer --%s", path, flag)
		}
	}
}

// The listings that hold every row before rendering must not offer a cursor:
// they have no page boundary to resume from, so --after would name a position
// that does not exist.
func TestListingsThatHoldEveryRowOfferNoCursor(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"dataset list",
		"evaluator list",
	} {
		cmd := find(t, path)

		assert.NotNil(t, cmd.Flags().Lookup("limit"), "%s bounds what it shows", path)
		assert.NotNil(t, cmd.Flags().Lookup("all"), "%s can be asked for everything", path)
		assert.Nil(t, cmd.Flags().Lookup("after"),
			"%s walks every page, so there is no cursor to hand back", path)
	}
}

// --all and --after together is a contradiction: one asks for every page, the
// other for the page after a position. The flags stay independent so the
// combination is at least answerable, and --all wins by walking.
func TestJobListPrefersTheWalkWhenBothAreGiven(t *testing.T) {
	t.Parallel()

	cmd := find(t, "job list")
	require.NoError(t, cmd.Flags().Set("all", "true"))
	require.NoError(t, cmd.Flags().Set("after", "job_7"))

	all, err := cmd.Flags().GetBool("all")
	require.NoError(t, err)
	assert.True(t, all, "--all is what the walk branch reads")
}
