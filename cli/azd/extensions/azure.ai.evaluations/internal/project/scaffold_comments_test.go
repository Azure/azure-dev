// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Adding an eval to a configuration a reader maintains must leave their notes
// alone. This is the claim the bug bash instructions lead with.
func TestApplyScaffoldKeepsComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, EvalConfigBase)
	original := "# Hand-maintained. Do not reformat.\n" +
		"evals:\n" +
		"  # the eval that gates the release\n" +
		"  - name: first\n" +
		"    description: d\n"
	require.NoError(t, os.WriteFile(path, []byte(original), 0o600))

	err := ApplyScaffold(dir, ScaffoldWrite{
		Evals: []Eval{{Name: "second", Description: "d"}},
	})
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(got)
	t.Logf("result:\n%s", body)

	require.True(t, strings.Contains(body, "- name: second"), "the new eval was not added")
	require.True(t, strings.Contains(body, "# Hand-maintained. Do not reformat."),
		"the file-level comment was deleted")
	require.True(t, strings.Contains(body, "# the eval that gates the release"),
		"the inline comment was deleted")
}
