// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// zipEntry describes a single file or directory entry to add to a test
// archive. Body is ignored for directories (where Name ends with "/").
type zipEntry struct {
	Name string
	Body []byte
	// Mode is the file mode; when zero we default to 0644 for files and
	// 0755 for directories.
	Mode os.FileMode
}

// makeZip builds an in-memory ZIP archive from entries.
func makeZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		mode := e.Mode
		isDir := mode.IsDir() || (len(e.Name) > 0 && e.Name[len(e.Name)-1] == '/')
		if mode == 0 {
			if isDir {
				mode = os.ModeDir | 0755
			} else {
				mode = 0644
			}
		}
		hdr := &zip.FileHeader{Name: e.Name, Method: zip.Deflate}
		hdr.SetMode(mode)
		w, err := zw.CreateHeader(hdr)
		require.NoError(t, err)
		if !isDir && len(e.Body) > 0 {
			_, err := w.Write(e.Body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// tarEntry describes a tar entry for test archives.
type tarEntry struct {
	Name string
	Body []byte
}

// makeTarGz builds an in-memory gzip-compressed tar archive.
func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.Name, Mode: 0644, Typeflag: tar.TypeReg, Size: int64(len(e.Body))}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write(e.Body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func TestSafeExtract_HappyPath(t *testing.T) {
	archive := makeZip(t, []zipEntry{
		{Name: "SKILL.md", Body: []byte("---\nname: foo\n---\nbody\n")},
		{Name: "assets/"},
		{Name: "assets/icon.svg", Body: []byte("<svg/>")},
		{Name: "assets/notes.txt", Body: []byte("hello")},
	})
	dir := t.TempDir()

	res, err := SafeExtract(archive, ExtractOptions{OutputDir: dir})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"SKILL.md", "assets/icon.svg", "assets/notes.txt"}, res.Files)

	got, _ := os.ReadFile(filepath.Join(dir, "assets", "icon.svg")) //nolint:gosec // test artifact
	require.Equal(t, "<svg/>", string(got))
}

func TestSafeExtract_RejectsDotDot(t *testing.T) {
	archive := makeZip(t, []zipEntry{{Name: "../evil.txt", Body: []byte("nope")}})
	_, err := SafeExtract(archive, ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsafeEntry))
}

func TestSafeExtract_RejectsAbsolutePath(t *testing.T) {
	archive := makeZip(t, []zipEntry{{Name: "/etc/passwd", Body: []byte("nope")}})
	_, err := SafeExtract(archive, ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsafeEntry))
}

func TestSafeExtract_RejectsSymlink(t *testing.T) {
	// Build a zip entry whose mode declares a symlink. The CLI must refuse
	// to extract this and must not follow the link target.
	archive := makeZip(t, []zipEntry{{
		Name: "link",
		Body: []byte("target"),
		Mode: os.ModeSymlink | 0777,
	}})
	_, err := SafeExtract(archive, ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsafeEntry))
}

func TestSafeExtract_RejectsWindowsBackslash(t *testing.T) {
	archive := makeZip(t, []zipEntry{{Name: `..\evil.txt`, Body: []byte("nope")}})
	_, err := SafeExtract(archive, ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsafeEntry))
}

func TestSafeExtract_EnforcesEntryCount(t *testing.T) {
	entries := make([]zipEntry, 5)
	for i := range entries {
		entries[i] = zipEntry{Name: filepath.ToSlash(filepath.Join("entry", string(rune('a'+i))+".txt"))}
	}
	archive := makeZip(t, entries)
	_, err := SafeExtract(archive, ExtractOptions{
		OutputDir:  t.TempDir(),
		MaxEntries: 2,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrLimitExceeded))
}

func TestSafeExtract_EnforcesTotalSize(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 1024)
	archive := makeZip(t, []zipEntry{{Name: "big.txt", Body: body}})
	_, err := SafeExtract(archive, ExtractOptions{
		OutputDir:            t.TempDir(),
		MaxTotalUncompressed: 100,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrLimitExceeded))
}

func TestSafeExtract_RejectsCollisionWithoutForce(t *testing.T) {
	archive := makeZip(t, []zipEntry{{Name: "SKILL.md", Body: []byte("body")}})
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("old"), 0600))

	_, err := SafeExtract(archive, ExtractOptions{OutputDir: dir})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCollision))

	// Existing file is untouched.
	existing, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")) //nolint:gosec // test artifact
	require.Equal(t, "old", string(existing))
}

func TestSafeExtract_ForceOverwrites(t *testing.T) {
	archive := makeZip(t, []zipEntry{{Name: "SKILL.md", Body: []byte("new")}})
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("old"), 0600))

	_, err := SafeExtract(archive, ExtractOptions{OutputDir: dir, Force: true})
	require.NoError(t, err)

	got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")) //nolint:gosec // test artifact
	require.Equal(t, "new", string(got))
}

func TestSafeExtract_InvalidArchive(t *testing.T) {
	_, err := SafeExtract([]byte("not a zip stream"), ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInvalidArchive))
}

func TestSafeExtract_EmptyBody(t *testing.T) {
	_, err := SafeExtract(nil, ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInvalidArchive))
}

func TestSafeExtract_TarGzHappyPath(t *testing.T) {
	archive := makeTarGz(t, []tarEntry{
		{Name: "SKILL.md", Body: []byte("---\nname: foo\n---\nbody\n")},
		{Name: "assets/icon.svg", Body: []byte("<svg/>")},
	})
	dir := t.TempDir()
	res, err := SafeExtract(archive, ExtractOptions{OutputDir: dir})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"SKILL.md", "assets/icon.svg"}, res.Files)
}

func TestDetectArchiveFormat(t *testing.T) {
	zipBytes := makeZip(t, []zipEntry{{Name: "SKILL.md", Body: []byte("body")}})
	require.Equal(t, ArchiveZip, DetectArchiveFormat(zipBytes))

	tarGzBytes := makeTarGz(t, []tarEntry{{Name: "SKILL.md", Body: []byte("body")}})
	require.Equal(t, ArchiveTarGz, DetectArchiveFormat(tarGzBytes))

	require.Equal(t, ArchiveUnknown, DetectArchiveFormat([]byte("not an archive")))
	require.Equal(t, ArchiveUnknown, DetectArchiveFormat(nil))
}

func TestValidateEntryName(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"SKILL.md", "SKILL.md", true},
		{"assets/icon.svg", "assets/icon.svg", true},
		{"./SKILL.md", "SKILL.md", true},
		{"assets/", "assets", true},
		{"", "", false},
		{".", "", false},
		{"/", "", false},
		{"/etc/passwd", "", false},
		{"../evil", "", false},
		{"a/../b", "", false},
		{`C:\Windows\Temp`, "", false},
	}
	for _, c := range cases {
		got, ok := validateEntryName(c.in)
		require.Equal(t, c.wantOK, ok, "input=%q", c.in)
		require.Equal(t, c.want, got, "input=%q", c.in)
	}
}
