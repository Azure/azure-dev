// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyDirectory_RefusesToCopyIntoSubtree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(src, "child")

	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write src file: %v", err)
	}

	if err := copyDirectory(src, dst); err == nil {
		t.Fatalf("expected error when destination is inside source")
	}
}

func TestCopyDirectory_NoOpWhenSamePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if err := copyDirectory(dir, dir); err != nil {
		t.Fatalf("expected no error when src==dst: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "file.txt")); err != nil {
		t.Fatalf("expected file to still exist: %v", err)
	}
}

func TestValidateLocalContainerAgentCopy_AllowsReinitInPlace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPointer := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(manifestPointer, []byte("name: test"), 0644); err != nil {
		t.Fatalf("write agent.yaml: %v", err)
	}

	// InitAction with nil azdClient is safe here because isSamePath returns early
	// before any prompting code is reached.
	a := &InitAction{}
	if err := a.validateLocalContainerAgentCopy(context.Background(), manifestPointer, dir); err != nil {
		t.Fatalf("expected no error for re-init in place: %v", err)
	}
}

func TestIsSubpath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		child    string
		parent   string
		expected bool
	}{
		{"child inside parent", "/a/b/c", "/a/b", true},
		{"child equals parent", "/a/b", "/a/b", true},
		{"child outside parent", "/a/b", "/a/b/c", false},
		{"sibling directories", "/a/b", "/a/c", false},
		{"parent with trailing slash", "/a/b/c", "/a/b/", true},
		{"relative same", ".", ".", true},
		{"relative child", "a/b", "a", true},
		{"relative outside", "a", "a/b", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSubpath(tt.child, tt.parent)
			if result != tt.expected {
				t.Errorf("isSubpath(%q, %q) = %v, want %v", tt.child, tt.parent, result, tt.expected)
			}
		})
	}
}

func TestIsSamePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a        string
		b        string
		expected bool
	}{
		{"identical paths", "/a/b/c", "/a/b/c", true},
		{"trailing slash difference", "/a/b/c", "/a/b/c/", true},
		{"with dot segments", "/a/b/../b/c", "/a/b/c", true},
		{"different paths", "/a/b", "/a/c", false},
		{"relative same", "a/b", "a/b", true},
		{"relative with dots", "a/b/../b", "a/b", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSamePath(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("isSamePath(%q, %q) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

type mockDirEntry struct {
	name  string
	isDir bool
}

func (m mockDirEntry) Name() string               { return m.name }
func (m mockDirEntry) IsDir() bool                { return m.isDir }
func (m mockDirEntry) Type() os.FileMode          { return 0 }
func (m mockDirEntry) Info() (os.FileInfo, error) { return nil, nil }

func TestFormatDirectoryPreview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		entries    []os.DirEntry
		maxEntries int
		expected   string
	}{
		{
			name:       "empty entries",
			entries:    []os.DirEntry{},
			maxEntries: 5,
			expected:   "",
		},
		{
			name: "fewer than max",
			entries: []os.DirEntry{
				mockDirEntry{name: "file.txt", isDir: false},
				mockDirEntry{name: "dir", isDir: true},
			},
			maxEntries: 5,
			expected:   "dir/, file.txt",
		},
		{
			name: "exactly max",
			entries: []os.DirEntry{
				mockDirEntry{name: "a.txt", isDir: false},
				mockDirEntry{name: "b.txt", isDir: false},
			},
			maxEntries: 2,
			expected:   "a.txt, b.txt",
		},
		{
			name: "more than max",
			entries: []os.DirEntry{
				mockDirEntry{name: "c.txt", isDir: false},
				mockDirEntry{name: "a.txt", isDir: false},
				mockDirEntry{name: "b.txt", isDir: false},
				mockDirEntry{name: "d.txt", isDir: false},
			},
			maxEntries: 2,
			expected:   "a.txt, b.txt, ... (+2 more)",
		},
		{
			name: "directories get trailing slash",
			entries: []os.DirEntry{
				mockDirEntry{name: "mydir", isDir: true},
				mockDirEntry{name: "myfile", isDir: false},
			},
			maxEntries: 5,
			expected:   "mydir/, myfile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := formatDirectoryPreview(tt.entries, tt.maxEntries)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("formatDirectoryPreview() = %q, want %q", result, tt.expected)
			}
		})
	}
}
