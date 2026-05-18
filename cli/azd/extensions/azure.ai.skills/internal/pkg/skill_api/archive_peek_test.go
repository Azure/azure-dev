// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPeekArchiveSkillName_RootLevel(t *testing.T) {
	archive := makeZip(t, []zipEntry{
		{Name: "SKILL.md", Body: []byte("---\nname: foo\n---\nbody\n")},
		{Name: "other.txt", Body: []byte("ignored")},
	})
	got, err := PeekArchiveSkillName(archive)
	require.NoError(t, err)
	require.Equal(t, "foo", got)
}

func TestPeekArchiveSkillName_OneDirDeep(t *testing.T) {
	archive := makeZip(t, []zipEntry{
		{Name: "greeting/"},
		{Name: "greeting/SKILL.md", Body: []byte("---\nname: greeting\n---\nbody\n")},
	})
	got, err := PeekArchiveSkillName(archive)
	require.NoError(t, err)
	require.Equal(t, "greeting", got)
}

func TestPeekArchiveSkillName_TooDeepIgnored(t *testing.T) {
	archive := makeZip(t, []zipEntry{
		{Name: "a/b/SKILL.md", Body: []byte("---\nname: deep\n---\nbody\n")},
	})
	got, err := PeekArchiveSkillName(archive)
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestPeekArchiveSkillName_NoSkillMd(t *testing.T) {
	archive := makeZip(t, []zipEntry{{Name: "README.md", Body: []byte("hi")}})
	got, err := PeekArchiveSkillName(archive)
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestPeekArchiveSkillName_MissingNameField(t *testing.T) {
	archive := makeZip(t, []zipEntry{
		{Name: "SKILL.md", Body: []byte("---\ndescription: hi\n---\nbody\n")},
	})
	got, err := PeekArchiveSkillName(archive)
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestPeekArchiveSkillName_MalformedYAMLReturnsEmpty(t *testing.T) {
	archive := makeZip(t, []zipEntry{
		{Name: "SKILL.md", Body: []byte("not valid front matter")},
	})
	got, err := PeekArchiveSkillName(archive)
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestPeekArchiveSkillName_InvalidZip(t *testing.T) {
	_, err := PeekArchiveSkillName([]byte("this is not zip"))
	require.Error(t, err)
}
