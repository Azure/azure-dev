// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyDirectory(t *testing.T) {
	t.Parallel()

	t.Run("happy_path", func(t *testing.T) {
		t.Parallel()
		src := t.TempDir()

		// Create a small tree: file.txt, sub/nested.txt
		if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		subDir := filepath.Join(src, "sub")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("world"), 0644); err != nil {
			t.Fatal(err)
		}

		dst := filepath.Join(t.TempDir(), "out")
		if err := copyDirectory(src, dst); err != nil {
			t.Fatal(err)
		}

		// Verify top-level file
		assertFileContents(t, filepath.Join(dst, "file.txt"), "hello")
		// Verify nested file
		assertFileContents(t, filepath.Join(dst, "sub", "nested.txt"), "world")
	})

	t.Run("same_path_noop", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := copyDirectory(dir, dir); err != nil {
			t.Fatalf("expected nil error for same path, got %v", err)
		}
	})

	t.Run("subpath_error", func(t *testing.T) {
		t.Parallel()
		src := t.TempDir()
		dst := filepath.Join(src, "child")
		if err := os.MkdirAll(dst, 0755); err != nil {
			t.Fatal(err)
		}

		err := copyDirectory(src, dst)
		if err == nil {
			t.Fatal("expected error when dst is subpath of src")
		}
		if !strings.Contains(err.Error(), "refusing to copy") {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("missing_source_error", func(t *testing.T) {
		t.Parallel()
		src := filepath.Join(t.TempDir(), "nonexistent")
		dst := t.TempDir()

		err := copyDirectory(src, dst)
		if err == nil {
			t.Fatal("expected error for missing source")
		}
	})
}

func TestCopyFile(t *testing.T) {
	t.Parallel()

	t.Run("happy_path", func(t *testing.T) {
		t.Parallel()
		src := filepath.Join(t.TempDir(), "src.txt")
		if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}

		dst := filepath.Join(t.TempDir(), "dst.txt")
		if err := copyFile(src, dst); err != nil {
			t.Fatal(err)
		}
		assertFileContents(t, dst, "data")
	})

	t.Run("missing_source_error", func(t *testing.T) {
		t.Parallel()
		src := filepath.Join(t.TempDir(), "nope.txt")
		dst := filepath.Join(t.TempDir(), "dst.txt")

		if err := copyFile(src, dst); err == nil {
			t.Fatal("expected error for missing source file")
		}
	})
}

// assertFileContents is a test helper that reads a file and compares its contents.
func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if got := string(data); got != want {
		t.Errorf("file %s: got %q, want %q", path, got, want)
	}
}

