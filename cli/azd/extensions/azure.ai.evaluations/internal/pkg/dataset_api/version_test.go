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
