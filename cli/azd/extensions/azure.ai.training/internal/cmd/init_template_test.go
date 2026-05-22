// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsGitHubUrl(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"github.com blob URL", "https://github.com/Azure/azure-dev/blob/main/job.yaml", true},
		{"raw.githubusercontent.com URL", "https://raw.githubusercontent.com/Azure/azure-dev/main/job.yaml", true},
		{"api.github.com URL", "https://api.github.com/repos/Azure/azure-dev/contents/job.yaml", true},
		{"github enterprise URL", "https://github.contoso.com/org/repo/blob/main/job.yaml", true},
		{"non-github https URL", "https://example.com/job.yaml", false},
		{"local relative path", "./templates/job.yaml", false},
		{"local absolute path (unix)", "/tmp/templates", false},
		{"empty string", "", false},
		{"malformed url", "://not a url", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGitHubUrl(tc.url); got != tc.want {
				t.Errorf("isGitHubUrl(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func TestCopyFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(src, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Run("copies content into nested destination", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "nested", "dir", "out.txt")
		if err := copyFile(src, dst); err != nil {
			t.Fatalf("copyFile: %v", err)
		}
		// #nosec G304 -- dst is built from t.TempDir(); test-only file read
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read dst: %v", err)
		}
		if string(got) != "hello" {
			t.Errorf("dst content = %q, want %q", string(got), "hello")
		}
	})

	t.Run("missing source returns error", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "out.txt")
		if err := copyFile(filepath.Join(t.TempDir(), "missing.txt"), dst); err == nil {
			t.Error("expected error for missing source, got nil")
		}
	})
}

func TestCopyDirectory(t *testing.T) {
	src := t.TempDir()
	// src/
	//   a.txt
	//   sub/
	//     b.txt
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}

	dst := t.TempDir()
	if err := copyDirectory(src, dst); err != nil {
		t.Fatalf("copyDirectory: %v", err)
	}

	// #nosec G304 -- dst is t.TempDir(); test-only file read
	a, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil || string(a) != "a" {
		t.Errorf("a.txt: got %q err %v", string(a), err)
	}
	// #nosec G304 -- dst is t.TempDir(); test-only file read
	b, err := os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	if err != nil || string(b) != "b" {
		t.Errorf("sub/b.txt: got %q err %v", string(b), err)
	}
}

func TestScaffoldFromLocalPath(t *testing.T) {
	t.Run("copies template directory contents", func(t *testing.T) {
		src := t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "job.yaml"), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		dst := t.TempDir()
		if err := scaffoldFromLocalPath(src, dst); err != nil {
			t.Fatalf("scaffoldFromLocalPath: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dst, "job.yaml")); err != nil {
			t.Errorf("expected job.yaml in dst, got: %v", err)
		}
	})

	t.Run("non-existent path returns error", func(t *testing.T) {
		dst := t.TempDir()
		err := scaffoldFromLocalPath(filepath.Join(t.TempDir(), "does-not-exist"), dst)
		if err == nil {
			t.Error("expected error for missing src, got nil")
		}
	})

	t.Run("file (not directory) returns error", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "file.txt")
		if err := os.WriteFile(f, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		dst := t.TempDir()
		err := scaffoldFromLocalPath(f, dst)
		if err == nil {
			t.Error("expected error when src is a file, got nil")
		}
	})
}
