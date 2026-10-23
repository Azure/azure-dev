// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azure.ai.rle/internal/project"
)

// useRleManifestDir switches to a fresh temp directory holding a valid rle.toml,
// which publish, train and rollout require in the working directory.
func useRleManifestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	schemaVersion := project.CurrentRleManifestSchemaVersion
	if err := project.WriteRleConfig(dir, project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle:           project.RleManifest{Name: "code_rl", Version: "1.0.0"},
		Harness: project.RleHarnessManifest{
			Type: project.HarnessTypeGym,
		},
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// testFoundryProjectPath is the Foundry project path segment used across test fixtures
// that stub the Foundry project endpoint (e.g. "https://rle.test/api/projects/project-1").
const testFoundryProjectPath = "/api/projects/project-1"
