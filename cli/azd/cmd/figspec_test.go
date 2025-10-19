// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/figspec"
	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/stretchr/testify/require"
)

// TestFigSpec generates a Fig autocomplete spec for azd, powering VS Code terminal IntelliSense.
// The generated TypeScript spec must be committed to the vscode repository to enable completions.
//
// To update snapshots (assuming your current directory is cli/azd):
//
// For Bash,
// UPDATE_SNAPSHOTS=true go test ./cmd -run TestFigSpec
//
// For Pwsh,
// $env:UPDATE_SNAPSHOTS='true'; go test ./cmd -run TestFigSpec; $env:UPDATE_SNAPSHOTS=$null
func TestFigSpec(t *testing.T) {
	root := NewRootCmd(false, nil, nil)

	builder := figspec.NewSpecBuilder(false)
	spec := builder.BuildSpec(root)

	typescript, err := spec.ToTypeScript()
	require.NoError(t, err)

	snapshotter := snapshot.NewConfig(".ts")
	snapshotter.SnapshotT(t, typescript)
}
