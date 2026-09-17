// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A version is a sequence of numbers, not a decimal fraction, and only looks
// like one while the minor stays under ten.
//
// ParseFloat read "1.10" as one-point-one and sorted it below "1.9", so after
// ten publishes `show`, `download` and `delete` all resolved "latest" to the
// older version and acted on it without saying so.
func TestTenComesAfterNine(t *testing.T) {
	assert.True(t, VersionGreater("1.10", "1.9"),
		"1.10 is the tenth minor, not one-point-one")
	assert.False(t, VersionGreater("1.9", "1.10"))

	assert.Equal(t, "1.10", LatestVersion([]Dataset{
		{Version: "1.0"}, {Version: "1.9"}, {Version: "1.10"}, {Version: "1.2"},
	}))
}

// The ordinary sequence this CLI publishes still orders the way it did.
func TestTheUsualSequenceIsUnchanged(t *testing.T) {
	assert.True(t, VersionGreater("2.0", "1.0"))
	assert.True(t, VersionGreater("10.0", "9.0"))
	assert.False(t, VersionGreater("1.0", "1.0"))

	assert.Equal(t, "3.0", LatestVersion([]Dataset{
		{Version: "1.0"}, {Version: "3.0"}, {Version: "2.0"},
	}))
}

// A version with more components is the later one where they agree so far,
// which is what treating the missing component as zero says.
func TestMoreComponentsSortAfterFewer(t *testing.T) {
	assert.True(t, VersionGreater("1.2.1", "1.2"))
	assert.False(t, VersionGreater("1.2", "1.2.1"))
	assert.False(t, VersionGreater("1.2.0", "1.2"),
		"a trailing zero adds nothing, so neither is newer")
}

// An unorderable version never wins and never triggers a comparison on its own.
func TestUnorderableVersionsAreNotOrdered(t *testing.T) {
	assert.Nil(t, VersionOrder(""))
	assert.Nil(t, VersionOrder("latest"))
	assert.False(t, VersionGreater("latest", "1.0"))
	assert.False(t, VersionGreater("1.0", "latest"))

	assert.Equal(t, "2.0", LatestVersion([]Dataset{
		{Version: "latest"}, {Version: "2.0"}, {Version: "1.0"},
	}), "the ones that can be ordered still decide")
}

// Nothing orderable at all falls back to the last entry rather than to nothing.
func TestAllUnorderableFallsBackToTheLastEntry(t *testing.T) {
	assert.Equal(t, "beta", LatestVersion([]Dataset{
		{Version: "latest"}, {Version: "beta"},
	}))
	assert.Empty(t, LatestVersion(nil))
}

// A trailing-digit form is still ordered, which is what kept "v3" working.
func TestTrailingDigitsAreStillRead(t *testing.T) {
	assert.Equal(t, []int{3}, VersionOrder("v3"))
	assert.True(t, VersionGreater("v10", "v9"))
}
