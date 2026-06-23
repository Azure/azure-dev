// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// symlinkAvailable reports whether the OS supports creating symlinks. On
// Windows without Developer Mode, os.Symlink fails with a privilege error;
// the symlink-rejection tests are skipped in that environment.
func symlinkAvailable(t *testing.T) bool {
	t.Helper()
	tmp := t.TempDir()
	return os.Symlink(".", filepath.Join(tmp, "testlink")) == nil
}

func TestArchiveDirectory_HappyPathRoundTrips(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "SKILL.md"),
		[]byte("---\nname: my-skill\ndescription: hi\n---\nbody\n"),
		0600,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "assets"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(src, "assets", "icon.svg"), []byte("<svg/>"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "assets", "notes.txt"), []byte("hello"), 0600))

	data, err := ArchiveDirectory(src, ArchiveOptions{})
	require.NoError(t, err)
	require.True(t, isZipMagic(data))

	// Round-trip through SafeExtract: every file should land in dst with the
	// same content, using forward-slash paths regardless of the OS.
	dst := t.TempDir()
	result, err := SafeExtract(data, ExtractOptions{OutputDir: dst})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"SKILL.md",
		"assets/icon.svg",
		"assets/notes.txt",
	}, result.Files)
	require.FileExists(t, filepath.Join(dst, "SKILL.md"))
	require.FileExists(t, filepath.Join(dst, "assets", "icon.svg"))
	require.FileExists(t, filepath.Join(dst, "assets", "notes.txt"))
}

func TestArchiveDirectory_RejectsNonDirectory(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "not-a-dir.txt")
	require.NoError(t, os.WriteFile(f, []byte("not a dir"), 0600))
	_, err := ArchiveDirectory(f, ArchiveOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidArchive)
}

func TestArchiveDirectory_RejectsEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := ArchiveDirectory(dir, ArchiveOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidArchive)
}

func TestArchiveDirectory_FollowsTopLevelSymlink(t *testing.T) {
	if !symlinkAvailable(t) {
		t.Skip("OS does not allow creating symlinks")
	}
	real := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(real, "SKILL.md"), []byte("---\nname: s\n---\n"), 0600))

	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "link-to-real")
	require.NoError(t, os.Symlink(real, link))

	data, err := ArchiveDirectory(link, ArchiveOptions{})
	require.NoError(t, err)
	require.True(t, isZipMagic(data))
}

func TestArchiveDirectory_RejectsInnerSymlink(t *testing.T) {
	if !symlinkAvailable(t) {
		t.Skip("OS does not allow creating symlinks")
	}
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: s\n---\n"), 0600))

	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "secret"), []byte("oops"), 0600))
	require.NoError(t, os.Symlink(filepath.Join(target, "secret"), filepath.Join(src, "leak")))

	_, err := ArchiveDirectory(src, ArchiveOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnsafeEntry)
}

func TestArchiveDirectory_EnforcesEntryCap(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: s\n---\n"), 0600))
	for i := range 5 {
		name := filepath.Join(src, "file"+string(rune('a'+i))+".txt")
		require.NoError(t, os.WriteFile(name, []byte("x"), 0600))
	}
	_, err := ArchiveDirectory(src, ArchiveOptions{MaxEntries: 3})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrLimitExceeded)
}

func TestArchiveDirectory_EnforcesByteCap(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: s\n---\n"), 0600))
	// One ~200 byte file with a 100 byte total cap must trip ErrLimitExceeded.
	require.NoError(t, os.WriteFile(
		filepath.Join(src, "big.txt"),
		[]byte(strings.Repeat("A", 200)),
		0600,
	))
	_, err := ArchiveDirectory(src, ArchiveOptions{MaxTotalUncompressed: 100})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrLimitExceeded)
}

func TestArchiveDirectory_PreservesNestedPathsForwardSlash(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: s\n---\n"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(src, "a", "b", "c"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(src, "a", "b", "c", "deep.txt"), []byte("d"), 0600))

	data, err := ArchiveDirectory(src, ArchiveOptions{})
	require.NoError(t, err)

	dst := t.TempDir()
	result, err := SafeExtract(data, ExtractOptions{OutputDir: dst})
	require.NoError(t, err)
	// Path inside the archive uses forward slashes.
	require.Contains(t, result.Files, "a/b/c/deep.txt")
}

func TestLocateSkillMdInDir_FoundAtRoot(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, SkillMdFileName)
	require.NoError(t, os.WriteFile(want, []byte("---\nname: s\n---\n"), 0600))

	got, found, err := LocateSkillMdInDir(dir)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want, got)
}

func TestLocateSkillMdInDir_Missing(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0600))

	got, found, err := LocateSkillMdInDir(dir)
	require.NoError(t, err)
	require.False(t, found)
	require.Empty(t, got)
}

func TestLocateSkillMdInDir_NotShallow(t *testing.T) {
	// SKILL.md one directory deep must not be picked up: lookup is shallow
	// by design (see archive_create.go docstring).
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0700))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "nested", SkillMdFileName),
		[]byte("---\nname: s\n---\n"),
		0600,
	))
	_, found, err := LocateSkillMdInDir(dir)
	require.NoError(t, err)
	require.False(t, found)
}

func TestLocateSkillMdInDir_RejectsSymlink(t *testing.T) {
	if !symlinkAvailable(t) {
		t.Skip("OS does not allow creating symlinks")
	}
	target := t.TempDir()
	realFile := filepath.Join(target, "real-skill.md")
	require.NoError(t, os.WriteFile(realFile, []byte("---\nname: s\n---\n"), 0600))

	dir := t.TempDir()
	require.NoError(t, os.Symlink(realFile, filepath.Join(dir, SkillMdFileName)))

	_, _, err := LocateSkillMdInDir(dir)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrUnsafeEntry)
}

// TestArchiveDirectory_MissingDirPropagatesCause guards against a future
// refactor that might swallow the underlying fs.PathError cause when the
// source directory does not exist. The error is intentionally *not* a
// sentinel (it bubbles up from os.Stat), so callers can use errors.As to
// recover the path-level detail.
func TestArchiveDirectory_MissingDirPropagatesCause(t *testing.T) {
	_, err := ArchiveDirectory(filepath.Join(t.TempDir(), "does-not-exist"), ArchiveOptions{})
	require.Error(t, err)
	var pathErr *os.PathError
	require.True(t, errors.As(err, &pathErr) || strings.Contains(err.Error(), "stat"))
}
