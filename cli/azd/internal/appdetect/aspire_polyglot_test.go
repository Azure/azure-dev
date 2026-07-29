// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package appdetect

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
)

// writeFiles writes each name/content pair into dir and returns the directory entries, mirroring
// what the appdetect walker passes to detectors.
func writeFiles(t *testing.T, files map[string]string) (string, []fs.DirEntry) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		err := os.WriteFile(filepath.Join(dir, name), []byte(content), osutil.PermissionFile)
		require.NoError(t, err)
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	return dir, entries
}

func TestDetectAspirePolyglotAppHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		files      map[string]string
		expectOk   bool
		expectLang string
		expectFile string // base name only
	}{
		{
			name: "TypeScriptWithConfig",
			files: map[string]string{
				"apphost.mts":        "await createBuilder();",
				"package.json":       "{}",
				"aspire.config.json": `{"appHost":{"path":"apphost.mts","language":"typescript/nodejs"}}`,
			},
			expectOk:   true,
			expectLang: "typescript",
			expectFile: "apphost.mts",
		},
		{
			name: "TypeScriptByFileNameOnly",
			files: map[string]string{
				"apphost.ts":   "await createBuilder();",
				"package.json": "{}",
			},
			expectOk:   true,
			expectLang: "typescript",
			expectFile: "apphost.ts",
		},
		{
			name: "PythonWithConfig",
			files: map[string]string{
				"apphost.py":         "create_builder()",
				"aspire.config.json": `{"appHost":{"path":"apphost.py","language":"python"}}`,
			},
			expectOk:   true,
			expectLang: "python",
			expectFile: "apphost.py",
		},
		{
			name: "PythonWithCompanionButNoConfig",
			files: map[string]string{
				"apphost.py":               "create_builder()",
				"apphost_requirements.txt": "aspire",
			},
			expectOk:   true,
			expectLang: "python",
			expectFile: "apphost.py",
		},
		{
			name: "PythonAloneIsNotDetected",
			files: map[string]string{
				"apphost.py": "print('hello, not aspire')",
			},
			expectOk: false,
		},
		{
			name: "GoDetectedOnlyViaConfig",
			files: map[string]string{
				"apphost.go":         "package main",
				"aspire.config.json": `{"appHost":{"path":"apphost.go","language":"go"}}`,
			},
			expectOk:   true,
			expectLang: "go",
			expectFile: "apphost.go",
		},
		{
			name: "GoFileNameAloneIsNotDetected",
			files: map[string]string{
				"apphost.go": "package main",
				"go.mod":     "module example",
			},
			expectOk: false,
		},
		{
			name: "CSharpConfigIsNotPolyglot",
			files: map[string]string{
				"aspire.config.json": `{"appHost":{"path":"AppHost/AppHost.csproj"}}`,
			},
			expectOk: false,
		},
		{
			name: "PlainNodeAppIsNotDetected",
			files: map[string]string{
				"package.json": "{}",
				"index.ts":     "console.log('hi')",
			},
			expectOk: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir, entries := writeFiles(t, tt.files)
			lang, appHostFile, ok := detectAspirePolyglotAppHost(dir, entries)
			require.Equal(t, tt.expectOk, ok)
			if tt.expectOk {
				require.Equal(t, tt.expectLang, lang)
				require.Equal(t, tt.expectFile, filepath.Base(appHostFile))
			}
		})
	}
}

func TestDotNetAppHostDetector_PolyglotReturnsSuggestionError(t *testing.T) {
	t.Parallel()

	dir, entries := writeFiles(t, map[string]string{
		"apphost.mts":        "await createBuilder();",
		"package.json":       "{}",
		"aspire.config.json": `{"appHost":{"path":"apphost.mts","language":"typescript/nodejs"}}`,
	})

	detector := &dotNetAppHostDetector{}
	project, err := detector.DetectProject(t.Context(), dir, entries)
	require.Nil(t, project)
	require.Error(t, err)

	var suggestionErr *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, err, &suggestionErr)
	require.Contains(t, suggestionErr.Suggestion, "7138")
	require.NotEmpty(t, suggestionErr.Links)
}
