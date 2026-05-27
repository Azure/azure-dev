// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdignore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestLoad_NoFile(t *testing.T) {
	dir := t.TempDir()
	ig, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ig != nil {
		t.Fatalf("expected nil matcher when no .azdignore exists")
	}
}

func TestLoad_RejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows requires SeCreateSymbolicLinkPrivilege for non-admin
		// users; skip rather than special-case the test environment.
		t.Skip("symlink creation requires elevation on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(target, []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, FileName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("expected regular-file rejection, got: %v", err)
	}
}

func TestLoad_RejectsOversize(t *testing.T) {
	dir := t.TempDir()
	// Write MaxSize+1 bytes so Lstat sees an over-cap file immediately.
	data := make([]byte, MaxSize+1)
	for i := range data {
		data[i] = '#'
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("expected oversize rejection, got: %v", err)
	}
}

func TestLoad_StripsBOM(t *testing.T) {
	dir := t.TempDir()
	// BOM followed by a single ignore pattern.
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte("ignored.txt\n")...)
	if err := os.WriteFile(filepath.Join(dir, FileName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	ig, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ig == nil {
		t.Fatal("expected non-nil matcher")
	}
	// If the BOM had not been stripped, the first pattern would be
	// "\ufeffignored.txt" and would not match "ignored.txt".
	m := ig.Relative("ignored.txt", false)
	if m == nil || !m.Ignore() {
		t.Fatalf("expected BOM-stripped pattern to match ignored.txt")
	}
}

func TestApply_NoFile_NoOp(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(keep, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Apply(dir); err != nil {
		t.Fatalf("Apply returned error with no .azdignore: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("file should still exist: %v", err)
	}
}

func TestApply_RemovesMatchesAndIgnoreFile(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "keep.txt", "k")
	mustWrite(t, dir, "drop.txt", "d")
	mustWrite(t, dir, "docs/internal/secret.md", "s")
	mustWrite(t, dir, "docs/public/readme.md", "r")
	mustWrite(t, dir, FileName, "drop.txt\ndocs/internal/\n")

	if err := Apply(dir); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := walkRel(t, dir)
	want := []string{
		"docs",
		"docs/public",
		"docs/public/readme.md",
		"keep.txt",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected tree.\n got: %v\nwant: %v", got, want)
	}
}

func TestApply_RemovesNestedIgnoreFilesEvenWithoutRoot(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "keep.txt", "k")
	mustWrite(t, dir, "docs/.azdignore", "# nested - never processed but always removed\n")

	if err := Apply(dir); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := walkRel(t, dir)
	want := []string{
		"docs",
		"keep.txt",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected tree.\n got: %v\nwant: %v", got, want)
	}
}

func TestApply_RemovesRootAndNestedIgnoreFiles(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "keep.txt", "k")
	mustWrite(t, dir, "docs/.azdignore", "# nested\n")
	mustWrite(t, dir, FileName, "# empty rules, just cleanup\n")

	if err := Apply(dir); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := walkRel(t, dir)
	want := []string{
		"docs",
		"keep.txt",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected tree.\n got: %v\nwant: %v", got, want)
	}
}

func TestApply_NegationPattern(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, dir, "README.md", "r")
	mustWrite(t, dir, "NOTES.md", "n")
	mustWrite(t, dir, FileName, "*.md\n!README.md\n")

	if err := Apply(dir); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := walkRel(t, dir)
	want := []string{"README.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected tree.\n got: %v\nwant: %v", got, want)
	}
}

func TestFilter(t *testing.T) {
	rules := []byte("infra/keyvault.bicep\nazure.manifest.yaml\ninfra/secrets/\n")
	ig := ParseBytes(rules, ".")

	in := []string{
		"azure.yaml",
		"azure.manifest.yaml",
		"infra/main.bicep",
		"infra/keyvault.bicep",
		"infra/secrets/password.txt",
		"infra/secrets/nested/cert.pem",
		".azdignore",
		"docs/.azdignore",
	}
	got := Filter(in, ig)
	want := []string{
		"azure.yaml",
		"infra/main.bicep",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Filter unexpected.\n got: %v\nwant: %v", got, want)
	}
}

func TestIsIgnored(t *testing.T) {
	ig := ParseBytes([]byte("*.log\nvendor/\n!keep.log\n"), ".")
	tests := []struct {
		path string
		want bool
	}{
		{"src/main.go", false},
		{"src/debug.log", true},
		{"keep.log", false},
		{"vendor/lib.go", true},
		{"vendor/nested/lib.go", true},
		{"not-vendor.go", false},
	}
	for _, tc := range tests {
		if got := IsIgnored(ig, tc.path); got != tc.want {
			t.Errorf("IsIgnored(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestIsIgnored_NilMatcher(t *testing.T) {
	if IsIgnored(nil, "anything") {
		t.Fatal("nil matcher must not ignore anything")
	}
}

func TestFilter_NilMatcher_StripsIgnoreFilesOnly(t *testing.T) {
	in := []string{"a.txt", ".azdignore", "sub/.azdignore", "b.txt"}
	got := Filter(in, nil)
	want := []string{"a.txt", "b.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("Filter(nil) unexpected.\n got: %v\nwant: %v", got, want)
	}
}

func TestParseBytes_Empty(t *testing.T) {
	if got := ParseBytes(nil, "."); got != nil {
		t.Fatalf("expected nil matcher for empty input")
	}
	// BOM-only input collapses to empty.
	if got := ParseBytes([]byte{0xEF, 0xBB, 0xBF}, "."); got != nil {
		t.Fatalf("expected nil matcher for BOM-only input")
	}
}

// mustWrite creates a file (including parent directories) inside dir
// using forward-slash relative paths for cross-platform test fixtures.
func mustWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// walkRel returns the sorted list of paths under dir, relative to dir,
// using forward slashes so assertions are stable across platforms.
func walkRel(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	if err := filepath.WalkDir(dir, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if path == dir {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}
