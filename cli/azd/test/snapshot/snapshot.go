// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package snapshot contains adapters that azd uses to create snapshot tests.
//
// Snapshot with default configuration
//
//	func TestExample(t *testing.T) {
//	  result := someFunction()
//	  snapshot.SnapshotT(t, result)
//	}
//
// Snapshot with specific file extension
//
//	func TestExample(t *testing.T) {
//	  json := getJson()
//	  snapshotter := snapshot.NewConfig("json")
//	  snapshotter.SnapshotT(t, json)
//	}
//
// To update the snapshots simply set the UPDATE_SNAPSHOTS environment variable and run your tests e.g.
//
//	UPDATE_SNAPSHOTS=true go test ./...
//
// Your snapshot files will now have been updated to reflect the current output of your code.
package snapshot

import (
	"os"
	"testing"

	"github.com/bradleyjkemp/cupaloy/v2"
)

func init() {
	cupaloy.Global = NewDefaultConfig()
}

// NewDefaultConfig creates the default configuration that azd uses.
func NewDefaultConfig() *cupaloy.Config {
	isCi := os.Getenv("GITHUB_ACTIONS") == "true" ||
		os.Getenv("TF_BUILD") == "True"

	return cupaloy.NewDefaultConfig().
		// Always use testdata
		WithOptions(cupaloy.SnapshotSubdirectory("testdata")).
		// Configure default extension to .snap
		WithOptions(cupaloy.SnapshotFileExtension(".snap")).
		// Use go-spew instead of String() and Error() outputs
		WithOptions(cupaloy.UseStringerMethods(false)).
		// Fail update on CI, but allow local to succeed
		WithOptions(cupaloy.FailOnUpdate(isCi))
}

// SnapshotT creates a snapshot with the global config, and the current testing.T.
//
// SnapshotT compares the given variable to its previous value stored on the filesystem.
// test.Fatal is called with the diff if the snapshots do not match, or if a new snapshot was created.
//
// SnapshotT determines the snapshot file automatically from the name of the test.
// As a result it can be called at most once per test.
//
// If you want to call Snapshot multiple times in a test,
// collect the values and call Snapshot with all values at once.
func SnapshotT(t *testing.T, i ...interface{}) {
	cupaloy.SnapshotT(t, i...)
}

// NewConfig creates a new snapshot config with the supplied extension name.
//
// This can be useful when the snapshot generated is tied to a specific well-known file extension:
// json, html, ..., where diff tools will provide better support in diffing the changes.
func NewConfig(snapshotExtension string) *cupaloy.Config {
	return NewDefaultConfig().WithOptions(cupaloy.SnapshotFileExtension(snapshotExtension))
}
