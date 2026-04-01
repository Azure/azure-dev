// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package watch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileChange_String_Created(t *testing.T) {
	fc := FileChange{
		Path:       "/absolute/path/file.txt",
		ChangeType: FileCreated,
	}
	s := fc.String()
	require.Contains(t, s, "+ Created")
	require.Contains(t, s, "file.txt")
}

func TestFileChange_String_Modified(t *testing.T) {
	fc := FileChange{
		Path:       "/absolute/path/file.txt",
		ChangeType: FileModified,
	}
	s := fc.String()
	require.Contains(t, s, "± Modified")
}

func TestFileChange_String_Deleted(t *testing.T) {
	fc := FileChange{
		Path:       "/absolute/path/file.txt",
		ChangeType: FileDeleted,
	}
	s := fc.String()
	require.Contains(t, s, "- Deleted")
}

func TestFileChange_String_DefaultCase(t *testing.T) {
	fc := FileChange{
		Path:       "/absolute/path/unknown.txt",
		ChangeType: FileChangeType(99),
	}
	s := fc.String()
	// Default case should still contain the path
	require.Contains(t, s, "unknown.txt")
	// Should NOT contain the known prefixes
	require.NotContains(t, s, "Created")
	require.NotContains(t, s, "Modified")
	require.NotContains(t, s, "Deleted")
}

func TestFileChange_String_RelativePath(t *testing.T) {
	// When the file is inside the cwd, String() should
	// convert to a relative path. Use a known path that
	// exists relative to cwd.
	fc := FileChange{
		Path:       "relative.txt",
		ChangeType: FileCreated,
	}
	s := fc.String()
	// Should produce output regardless of relative conversion
	require.NotEmpty(t, s)
}

func TestFileChanges_String_Empty(t *testing.T) {
	fc := FileChanges{}
	require.Equal(t, "", fc.String())
}

func TestFileChanges_String_SingleEntry(t *testing.T) {
	fc := FileChanges{
		{Path: "file.txt", ChangeType: FileCreated},
	}
	s := fc.String()
	require.Contains(t, s, "Files changed:")
	require.Contains(t, s, "file.txt")
}

func TestFileChanges_String_MultipleEntries(t *testing.T) {
	fc := FileChanges{
		{Path: "a.txt", ChangeType: FileCreated},
		{Path: "b.txt", ChangeType: FileModified},
		{Path: "c.txt", ChangeType: FileDeleted},
	}
	s := fc.String()
	require.Contains(t, s, "Files changed:")
	require.Contains(t, s, "a.txt")
	require.Contains(t, s, "b.txt")
	require.Contains(t, s, "c.txt")
	require.Contains(t, s, "Created")
	require.Contains(t, s, "Modified")
	require.Contains(t, s, "Deleted")
}

func TestGetFileChanges_Sorting(t *testing.T) {
	fc := &fileChanges{
		Created:  map[string]bool{"z.txt": true, "a.txt": true},
		Modified: map[string]bool{"m.txt": true},
		Deleted:  map[string]bool{"d.txt": true},
	}
	fw := &fileWatcher{fileChanges: fc}

	changes := fw.GetFileChanges()
	require.Len(t, changes, 4)
	// Verify sorted by path
	for i := 1; i < len(changes); i++ {
		require.LessOrEqual(t, changes[i-1].Path, changes[i].Path,
			"changes should be sorted by path")
	}
}

func TestGetFileChanges_ChangeTypes(t *testing.T) {
	fc := &fileChanges{
		Created:  map[string]bool{"new.txt": true},
		Modified: map[string]bool{"mod.txt": true},
		Deleted:  map[string]bool{"del.txt": true},
	}
	fw := &fileWatcher{fileChanges: fc}

	changes := fw.GetFileChanges()
	changeMap := make(map[string]FileChangeType)
	for _, c := range changes {
		changeMap[c.Path] = c.ChangeType
	}

	require.Equal(t, FileDeleted, changeMap["del.txt"])
	require.Equal(t, FileModified, changeMap["mod.txt"])
	require.Equal(t, FileCreated, changeMap["new.txt"])
}

func TestGetFileChanges_EmptyMaps(t *testing.T) {
	fc := &fileChanges{
		Created:  map[string]bool{},
		Modified: map[string]bool{},
		Deleted:  map[string]bool{},
	}
	fw := &fileWatcher{fileChanges: fc}

	changes := fw.GetFileChanges()
	require.Empty(t, changes)
}

func TestFileChangeType_Exhaustive(t *testing.T) {
	tests := []struct {
		name string
		val  FileChangeType
		want int
	}{
		{"Created", FileCreated, 0},
		{"Modified", FileModified, 1},
		{"Deleted", FileDeleted, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, FileChangeType(tt.want), tt.val)
		})
	}
}

func TestFileChanges_String_PreservesOrder(t *testing.T) {
	// The String() method should iterate in the order of the
	// slice, so we can verify ordering is preserved.
	fc := FileChanges{
		{Path: "first.txt", ChangeType: FileCreated},
		{Path: "second.txt", ChangeType: FileModified},
		{Path: "third.txt", ChangeType: FileDeleted},
	}
	s := fc.String()

	firstIdx := indexOf(s, "first.txt")
	secondIdx := indexOf(s, "second.txt")
	thirdIdx := indexOf(s, "third.txt")

	require.Greater(t, secondIdx, firstIdx,
		"second should appear after first")
	require.Greater(t, thirdIdx, secondIdx,
		"third should appear after second")
}

// indexOf returns the position of substr in s, or -1.
func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
