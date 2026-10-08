// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A declared source is relative to the configuration, but a user may write an
// absolute one. `eval create` used to join it unconditionally, producing
// evals/C:/data/rows.jsonl, while `azd up` resolved it correctly -- so the same
// file worked or failed depending on which command published it.
func TestResolveSourceLeavesAnAbsolutePathAlone(t *testing.T) {
	abs := filepath.Join(string(filepath.Separator), "data", "rows.jsonl")
	if filepath.VolumeName(`C:\`) != "" {
		abs = `C:\data\rows.jsonl`
	}

	assert.Equal(t, abs, ResolveSource("evals", abs),
		"an absolute source is already where it says it is")
	assert.Equal(t, filepath.Join("evals", "datasets", "rows.jsonl"),
		ResolveSource("evals", "./datasets/rows.jsonl"),
		"a relative source hangs off the configuration's directory")
	assert.Empty(t, ResolveSource("evals", ""),
		"nothing declared stays nothing, rather than becoming the directory")
}
