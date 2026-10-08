// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Drift detection compares the version on the service with the one recorded at
// the last deploy, so the ordering has to be numeric rather than lexical:
// "10.0" is newer than "9.0" even though it sorts earlier as a string.
func TestVersionGreater(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"2.0", "1.0", true},
		{"1.0", "2.0", false},
		{"1.0", "1.0", false},
		{"10.0", "9.0", true},
		{"9.0", "10.0", false},
		{"v3", "v2", true},
	}

	for _, tc := range cases {
		require.Equal(t, tc.want, VersionGreater(tc.a, tc.b),
			"VersionGreater(%q, %q)", tc.a, tc.b)
	}
}

// An unorderable version must never trigger a drift failure on its own: the
// deploy would be blocked with no way for the author to reason about it.
func TestVersionGreaterIgnoresUnorderable(t *testing.T) {
	require.False(t, VersionGreater("draft", "1.0"))
	require.False(t, VersionGreater("1.0", "draft"))
	require.False(t, VersionGreater("", "1.0"))
	require.False(t, VersionGreater("1.0", ""))
}

// The two upload entry points read their version argument differently, and the
// difference is the whole point: UploadNewVersion counts from it, UploadVersion
// writes it. Passing "1.0" to the counting one publishes 2.0, which is not what
// an author who wrote version: "1.0" asked for.
func TestNextVersionCountsFromTheArgument(t *testing.T) {
	if got := NextVersion("1.0"); got != "2.0" {
		t.Fatalf("NextVersion(1.0) = %q, want 2.0", got)
	}
	if got := NextVersion("1"); got != "2.0" {
		t.Fatalf("NextVersion(1) = %q, want 2.0", got)
	}
	// An unknown current version starts the sequence rather than guessing.
	if got := NextVersion(""); got != "1.0" {
		t.Fatalf("NextVersion(empty) = %q, want 1.0", got)
	}
}

func TestLatestVersionOrdersNumerically(t *testing.T) {
	got := LatestVersion([]Dataset{{Version: "1.0"}, {Version: "10.0"}, {Version: "2.0"}})
	if got != "10.0" {
		t.Fatalf("LatestVersion = %q, want 10.0 (numeric, not lexical)", got)
	}
	if LatestVersion(nil) != "" {
		t.Fatal("LatestVersion(nil) should be empty")
	}
}

// LatestVersion documents a fallback to the last entry when nothing can be
// ordered. That fallback only runs if an unorderable version never becomes the
// running best, which a sentinel below -1 quietly prevented.
func TestLatestVersionFallsBackToTheLastEntryWhenNoneAreOrderable(t *testing.T) {
	got := LatestVersion([]Dataset{{Version: "alpha"}, {Version: "beta"}, {Version: "gamma"}})
	require.Equal(t, "gamma", got, "with nothing orderable the service's last entry wins")
}

func TestLatestVersionPrefersAnOrderableVersionOverAnUnorderableOne(t *testing.T) {
	require.Equal(t, "2.0", LatestVersion([]Dataset{{Version: "alpha"}, {Version: "2.0"}}))
	require.Equal(t, "2.0", LatestVersion([]Dataset{{Version: "2.0"}, {Version: "alpha"}}))
}
