// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows && !darwin

package processutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// T13: Linux marks an unlinked executable as "(deleted)" in /proc/<pid>/exe. Relocation
// creates exactly that state, so failing to strip the marker would hide the very
// processes this package exists to find.
func TestNormalizeProcExe_StripsDeletedSuffix(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"/home/user/.azd/extensions/demo/demo":           "/home/user/.azd/extensions/demo/demo",
		"/home/user/.azd/extensions/demo/demo (deleted)": "/home/user/.azd/extensions/demo/demo",
		"  /tmp/ext/tool (deleted)  ":                    "/tmp/ext/tool",
		"/tmp/ext/tool ":                                 "/tmp/ext/tool",
		"":                                               "",
		"   ":                                            "",
		// Only the trailing marker is removed. A path that genuinely contains the word
		// must survive intact, or a legitimately named binary would be mis-resolved.
		"/tmp/ext/ (deleted) tool": "/tmp/ext/ (deleted) tool",
	}

	for input, expected := range cases {
		require.Equalf(t, expected, normalizeProcExe(input), "normalizeProcExe(%q)", input)
	}
}
