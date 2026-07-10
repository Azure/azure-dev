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

	if err := scaffoldPromptConventionFolders(dir, "You are a triage assistant."); err != nil {
		t.Fatalf("scaffoldPromptConventionFolders: %v", err)
	}

	// instructions.md carries the provided instructions.
	content, err := os.ReadFile(filepath.Join(dir, "instructions.md"))
	if err != nil {
		t.Fatalf("read instructions.md: %v", err)
	}
	if string(content) != "You are a triage assistant.\n" {
		t.Errorf("instructions.md content: got %q", string(content))
	}

	// files/ and skills/ exist with a .gitkeep placeholder.
	for _, sub := range []string{"files", "skills"} {
		info, statErr := os.Stat(filepath.Join(dir, sub))
		if statErr != nil || !info.IsDir() {
			t.Errorf("%s/ should be a directory: %v", sub, statErr)
		}
		if _, keepErr := os.Stat(filepath.Join(dir, sub, ".gitkeep")); keepErr != nil {
			t.Errorf("%s/.gitkeep should exist: %v", sub, keepErr)
		}
	}
}

func TestScaffoldPromptConventionFolders_DefaultInstructions(t *testing.T) {
	dir := t.TempDir()
	if err := scaffoldPromptConventionFolders(dir, "   "); err != nil {
		t.Fatalf("scaffoldPromptConventionFolders: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "instructions.md"))
	if err != nil {
		t.Fatalf("read instructions.md: %v", err)
	}
	if string(content) != "You are a helpful AI assistant.\n" {
		t.Errorf("default instructions: got %q", string(content))
	}
}

func TestScaffoldPromptConventionFolders_DoesNotOverwriteInstructions(t *testing.T) {
	dir := t.TempDir()
	existing := "MY EDITED INSTRUCTIONS\n"
	if err := os.WriteFile(filepath.Join(dir, "instructions.md"), []byte(existing), 0o600); err != nil {
		t.Fatalf("seed instructions.md: %v", err)
	}

	if err := scaffoldPromptConventionFolders(dir, "should be ignored"); err != nil {
		t.Fatalf("scaffoldPromptConventionFolders: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "instructions.md"))
	if err != nil {
		t.Fatalf("read instructions.md: %v", err)
	}
	if string(content) != existing {
		t.Errorf("existing instructions.md should be preserved, got %q", string(content))
	}
}
