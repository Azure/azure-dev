// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/azdignore"
)

// TestCopyDirectoryThenAzdIgnoreApply verifies the integration used by
// downloadAgentYaml for the local ContainerAgent path: copy a manifest
// directory and then apply .azdignore rules in-place. This is the
// observable behavior consumers depend on -- ignored files must not be
// present and the .azdignore file itself must be removed.
func TestCopyDirectoryThenAzdIgnoreApply(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	// Files we expect to keep.
	mustWriteFile(t, filepath.Join(src, "agent.yaml"), "name: test\n")
	mustWriteFile(t, filepath.Join(src, "keep.txt"), "keep")
	// Files we expect to be filtered.
	mustWriteFile(t, filepath.Join(src, "secrets.env"), "TOKEN=abc")
	mustWriteFile(t, filepath.Join(src, "ignored", "data.bin"), "x")
	// Nested ignore file -- pruned regardless of content.
	mustWriteFile(t, filepath.Join(src, "nested", azdignore.FileName), "noop\n")
	// Root ignore file -- controls filtering and is also removed.
	mustWriteFile(t, filepath.Join(src, azdignore.FileName),
		"*.env\nignored/\n")

	dst := filepath.Join(t.TempDir(), "out")
	if err := copyDirectory(src, dst); err != nil {
		t.Fatalf("copyDirectory: %v", err)
	}
	if err := azdignore.Apply(dst); err != nil {
		t.Fatalf("azdignore.Apply: %v", err)
	}

	// Kept files must exist.
	for _, rel := range []string{"agent.yaml", "keep.txt"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("expected %s to be retained: %v", rel, err)
		}
	}
	// Ignored files and ignore files must be gone.
	for _, rel := range []string{
		"secrets.env",
		filepath.Join("ignored", "data.bin"),
		"ignored",
		azdignore.FileName,
		filepath.Join("nested", azdignore.FileName),
	} {
		if _, err := os.Stat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, stat err: %v", rel, err)
		}
	}
}

// mustWriteFile writes a file (creating any parent dirs) or fails the
// test. Tests own these paths so 0o600/0o750 modes are intentional.
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
