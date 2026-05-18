// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"archive/tar"
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPeekArchiveSkillName_RootLevel(t *testing.T) {
	bodies := map[string][]byte{
		"SKILL.md": []byte("---\nname: my-skill\ndescription: d\n---\nbody\n"),
	}
	entries := []tar.Header{
		{Name: "SKILL.md", Mode: 0644, Typeflag: tar.TypeReg},
	}
	got, err := PeekArchiveSkillName(bytes.NewReader(makeTarGz(t, entries, bodies)))
	require.NoError(t, err)
	require.Equal(t, "my-skill", got)
}

func TestPeekArchiveSkillName_OneDirDeep(t *testing.T) {
	bodies := map[string][]byte{
		"pkg/SKILL.md": []byte("---\nname: nested-skill\ndescription: d\n---\nbody\n"),
	}
	entries := []tar.Header{
		{Name: "pkg/", Mode: 0755, Typeflag: tar.TypeDir},
		{Name: "pkg/SKILL.md", Mode: 0644, Typeflag: tar.TypeReg},
	}
	got, err := PeekArchiveSkillName(bytes.NewReader(makeTarGz(t, entries, bodies)))
	require.NoError(t, err)
	require.Equal(t, "nested-skill", got)
}

func TestPeekArchiveSkillName_TooDeepIgnored(t *testing.T) {
	bodies := map[string][]byte{
		"a/b/SKILL.md": []byte("---\nname: too-deep\ndescription: d\n---\nbody\n"),
	}
	entries := []tar.Header{
		{Name: "a/b/SKILL.md", Mode: 0644, Typeflag: tar.TypeReg},
	}
	got, err := PeekArchiveSkillName(bytes.NewReader(makeTarGz(t, entries, bodies)))
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestPeekArchiveSkillName_NoSkillMd(t *testing.T) {
	bodies := map[string][]byte{
		"readme.txt": []byte("hello"),
	}
	entries := []tar.Header{
		{Name: "readme.txt", Mode: 0644, Typeflag: tar.TypeReg},
	}
	got, err := PeekArchiveSkillName(bytes.NewReader(makeTarGz(t, entries, bodies)))
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestPeekArchiveSkillName_MissingNameField(t *testing.T) {
	bodies := map[string][]byte{
		"SKILL.md": []byte("---\ndescription: no name here\n---\nbody\n"),
	}
	entries := []tar.Header{
		{Name: "SKILL.md", Mode: 0644, Typeflag: tar.TypeReg},
	}
	got, err := PeekArchiveSkillName(bytes.NewReader(makeTarGz(t, entries, bodies)))
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestPeekArchiveSkillName_MalformedYAMLReturnsEmpty(t *testing.T) {
	bodies := map[string][]byte{
		"SKILL.md": []byte("not a valid skill md without front matter"),
	}
	entries := []tar.Header{
		{Name: "SKILL.md", Mode: 0644, Typeflag: tar.TypeReg},
	}
	got, err := PeekArchiveSkillName(bytes.NewReader(makeTarGz(t, entries, bodies)))
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestPeekArchiveSkillName_InvalidGzip(t *testing.T) {
	_, err := PeekArchiveSkillName(bytes.NewReader([]byte("this is not gzip")))
	require.Error(t, err)
}
