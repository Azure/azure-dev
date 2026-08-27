// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScaffoldPromptConventionFolders_CreatesLayout(t *testing.T) {
	dir := t.TempDir()

	if err := scaffoldPromptConventionFolders(dir); err != nil {
		t.Fatalf("scaffoldPromptConventionFolders: %v", err)
	}

	// skills/ and vector-assets/ exist with a .gitkeep placeholder.
	for _, sub := range []string{"skills", "vector-assets"} {
		info, statErr := os.Stat(filepath.Join(dir, sub))
		if statErr != nil || !info.IsDir() {
			t.Errorf("%s/ should be a directory: %v", sub, statErr)
		}
		if _, keepErr := os.Stat(filepath.Join(dir, sub, ".gitkeep")); keepErr != nil {
			t.Errorf("%s/.gitkeep should exist: %v", sub, keepErr)
		}
	}
}

// Instructions are written inline into agent.yaml, so a scaffold with nothing
// authored still produces a deployable agent rather than an empty prompt.
func TestPromptScaffoldInstructions_DefaultsWhenBlank(t *testing.T) {
	if got := promptScaffoldInstructions("   "); got != "You are a helpful AI assistant." {
		t.Errorf("default instructions: got %q", got)
	}
	if got := promptScaffoldInstructions(" authored \n"); got != "authored" {
		t.Errorf("authored instructions: got %q", got)
	}
}
