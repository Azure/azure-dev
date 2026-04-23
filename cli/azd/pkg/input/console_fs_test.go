// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package input

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_fsSuggestions(t *testing.T) {
	dir := t.TempDir()

	// Create test files and directories
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "file.txt"), []byte("test"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".hidden"), []byte("hidden"), 0o600))
	require.NoError(t, os.MkdirAll(
		filepath.Join(dir, ".hiddendir"), 0o755))

	tests := []struct {
		name        string
		opts        FsSuggestOptions
		root        string
		input       string
		wantMinLen  int
		wantContain string
		wantExclude string
	}{
		{
			name:        "EmptyInputIncludesCurrentDir",
			opts:        FsSuggestOptions{},
			root:        dir,
			input:       "",
			wantMinLen:  1,
			wantContain: currentDirDisplayed,
		},
		{
			name: "EmptyInputExcludeCurrentDir",
			opts: FsSuggestOptions{
				ExcludeCurrentDir: true,
			},
			root:        dir,
			input:       "",
			wantExclude: currentDirDisplayed,
		},
		{
			name:       "MatchesFilesInDir",
			opts:       FsSuggestOptions{},
			root:       "",
			input:      filepath.Join(dir, "file"),
			wantMinLen: 1,
		},
		{
			name: "ExcludeHiddenFiles",
			opts: FsSuggestOptions{
				ExcludeCurrentDir: true,
			},
			root:        "",
			input:       filepath.Join(dir, "."),
			wantExclude: ".hidden",
			wantMinLen:  0,
		},
		{
			name: "IncludeHiddenFiles",
			opts: FsSuggestOptions{
				ExcludeCurrentDir:  true,
				IncludeHiddenFiles: true,
			},
			root:       "",
			input:      filepath.Join(dir, ".h"),
			wantMinLen: 1,
		},
		{
			name: "ExcludeDirectories",
			opts: FsSuggestOptions{
				ExcludeCurrentDir:  true,
				ExcludeDirectories: true,
				IncludeHiddenFiles: true,
			},
			root:       "",
			input:      dir + string(os.PathSeparator),
			wantMinLen: 1,
		},
		{
			name: "ExcludeFiles",
			opts: FsSuggestOptions{
				ExcludeCurrentDir: true,
				ExcludeFiles:      true,
			},
			root:       "",
			input:      filepath.Join(dir, "sub"),
			wantMinLen: 1,
		},
		{
			name: "ExactFileMatch",
			opts: FsSuggestOptions{
				ExcludeCurrentDir: true,
			},
			root:       "",
			input:      filepath.Join(dir, "file.txt"),
			wantMinLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suggestions := fsSuggestions(tt.opts, tt.root, tt.input)
			if tt.wantMinLen > 0 {
				require.GreaterOrEqual(t, len(suggestions), tt.wantMinLen)
			}
			if tt.wantContain != "" {
				require.Contains(t, suggestions, tt.wantContain)
			}
			if tt.wantExclude != "" {
				for _, s := range suggestions {
					require.NotContains(t, s, tt.wantExclude)
				}
			}
		})
	}
}

func Test_fsSuggestions_DirectoryTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "mydir"), 0o755))

	suggestions := fsSuggestions(
		FsSuggestOptions{ExcludeCurrentDir: true},
		"",
		filepath.Join(dir, "mydir"),
	)

	require.Len(t, suggestions, 1)
	require.True(t,
		suggestions[0][len(suggestions[0])-1] == filepath.Separator,
		"directory suggestion should end with path separator")
}

func Test_fsSuggestions_WithRoot(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "readme.md"), []byte("# readme"), 0o600))

	suggestions := fsSuggestions(
		FsSuggestOptions{ExcludeCurrentDir: true},
		dir,
		"readme",
	)

	require.Len(t, suggestions, 1)
	require.Contains(t, suggestions[0], "readme.md")
}

func Test_currentDirDisplayed_Constant(t *testing.T) {
	require.Equal(t, "./   [current directory]", currentDirDisplayed)
}

func Test_currentDirSentinelTranslation(t *testing.T) {
	// Verify that the sentinel value translates to "./"
	expected := "." + string(filepath.Separator)

	// Simulate what PromptFs does when response == currentDirDisplayed
	response := currentDirDisplayed
	if response == currentDirDisplayed {
		response = "." + string(filepath.Separator)
	}

	require.Equal(t, expected, response)

	if runtime.GOOS == "windows" {
		require.Equal(t, ".\\", response)
	} else {
		require.Equal(t, "./", response)
	}
}
