// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// makeTarGz builds a gzip-compressed tar archive in memory containing the
// provided entries. Each entry's TypeFlag drives the tar header type.
func makeTarGz(t *testing.T, entries []tar.Header, bodies map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		// Set Size from body when present.
		if h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA {
			h.Size = int64(len(bodies[h.Name]))
		}
		require.NoError(t, tw.WriteHeader(&h))
		if body, ok := bodies[h.Name]; ok {
			_, err := tw.Write(body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func TestSafeExtract_HappyPath(t *testing.T) {
	bodies := map[string][]byte{
		"SKILL.md":         []byte("---\nname: foo\n---\nbody\n"),
		"assets/icon.svg":  []byte("<svg/>"),
		"assets/notes.txt": []byte("hello"),
	}
	entries := []tar.Header{
		{Name: "SKILL.md", Mode: 0644, Typeflag: tar.TypeReg},
		{Name: "assets/", Mode: 0755, Typeflag: tar.TypeDir},
		{Name: "assets/icon.svg", Mode: 0644, Typeflag: tar.TypeReg},
		{Name: "assets/notes.txt", Mode: 0644, Typeflag: tar.TypeReg},
	}
	archive := makeTarGz(t, entries, bodies)
	dir := t.TempDir()

	res, err := SafeExtract(bytes.NewReader(archive), ExtractOptions{OutputDir: dir})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"SKILL.md", "assets/icon.svg", "assets/notes.txt"}, res.Files)

	got, _ := os.ReadFile(filepath.Join(dir, "assets", "icon.svg")) //nolint:gosec // test artifact
	require.Equal(t, "<svg/>", string(got))
}

func TestSafeExtract_RejectsDotDot(t *testing.T) {
	archive := makeTarGz(t,
		[]tar.Header{{Name: "../evil.txt", Mode: 0644, Typeflag: tar.TypeReg}},
		map[string][]byte{"../evil.txt": []byte("nope")},
	)
	_, err := SafeExtract(bytes.NewReader(archive), ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsafeEntry))
}

func TestSafeExtract_RejectsAbsolutePath(t *testing.T) {
	archive := makeTarGz(t,
		[]tar.Header{{Name: "/etc/passwd", Mode: 0644, Typeflag: tar.TypeReg}},
		map[string][]byte{"/etc/passwd": []byte("nope")},
	)
	_, err := SafeExtract(bytes.NewReader(archive), ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsafeEntry))
}

func TestSafeExtract_RejectsSymlink(t *testing.T) {
	archive := makeTarGz(t,
		[]tar.Header{{Name: "link", Mode: 0777, Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}},
		nil,
	)
	_, err := SafeExtract(bytes.NewReader(archive), ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsafeEntry))
}

func TestSafeExtract_RejectsHardLink(t *testing.T) {
	archive := makeTarGz(t,
		[]tar.Header{{Name: "link", Mode: 0644, Linkname: "target", Typeflag: tar.TypeLink}},
		nil,
	)
	_, err := SafeExtract(bytes.NewReader(archive), ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsafeEntry))
}

func TestSafeExtract_RejectsWindowsBackslash(t *testing.T) {
	archive := makeTarGz(t,
		[]tar.Header{{Name: `..\evil.txt`, Mode: 0644, Typeflag: tar.TypeReg}},
		map[string][]byte{`..\evil.txt`: []byte("nope")},
	)
	_, err := SafeExtract(bytes.NewReader(archive), ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsafeEntry))
}

func TestSafeExtract_EnforcesEntryCount(t *testing.T) {
	headers := make([]tar.Header, 5)
	bodies := map[string][]byte{}
	for i := range headers {
		name := filepath.ToSlash(filepath.Join("entry", string(rune('a'+i))+".txt"))
		headers[i] = tar.Header{Name: name, Mode: 0644, Typeflag: tar.TypeReg}
		bodies[name] = []byte{}
	}
	archive := makeTarGz(t, headers, bodies)
	_, err := SafeExtract(bytes.NewReader(archive), ExtractOptions{
		OutputDir:  t.TempDir(),
		MaxEntries: 2,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrLimitExceeded))
}

func TestSafeExtract_EnforcesTotalSize(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 1024)
	archive := makeTarGz(t,
		[]tar.Header{{Name: "big.txt", Mode: 0644, Typeflag: tar.TypeReg}},
		map[string][]byte{"big.txt": body},
	)
	_, err := SafeExtract(bytes.NewReader(archive), ExtractOptions{
		OutputDir:            t.TempDir(),
		MaxTotalUncompressed: 100,
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrLimitExceeded))
}

func TestSafeExtract_RejectsCollisionWithoutForce(t *testing.T) {
	archive := makeTarGz(t,
		[]tar.Header{{Name: "SKILL.md", Mode: 0644, Typeflag: tar.TypeReg}},
		map[string][]byte{"SKILL.md": []byte("body")},
	)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("old"), 0600))

	_, err := SafeExtract(bytes.NewReader(archive), ExtractOptions{OutputDir: dir})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCollision))

	// Existing file is untouched.
	existing, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")) //nolint:gosec // test artifact
	require.Equal(t, "old", string(existing))
}

func TestSafeExtract_ForceOverwrites(t *testing.T) {
	archive := makeTarGz(t,
		[]tar.Header{{Name: "SKILL.md", Mode: 0644, Typeflag: tar.TypeReg}},
		map[string][]byte{"SKILL.md": []byte("new")},
	)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("old"), 0600))

	_, err := SafeExtract(bytes.NewReader(archive), ExtractOptions{OutputDir: dir, Force: true})
	require.NoError(t, err)

	got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")) //nolint:gosec // test artifact
	require.Equal(t, "new", string(got))
}

func TestSafeExtract_InvalidGzip(t *testing.T) {
	_, err := SafeExtract(bytes.NewReader([]byte("not a gzip stream")), ExtractOptions{OutputDir: t.TempDir()})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInvalidGzip))
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
