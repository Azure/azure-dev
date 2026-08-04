// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const agentsSynthesisPath = "../../../azure.ai.agents/internal/synthesis"

func TestAgentsSynthesisCopyMatches(t *testing.T) {
	t.Parallel()

	canonical := readSynthesisFiles(t, ".")
	agents := readSynthesisFiles(t, agentsSynthesisPath)

	assert.Equal(t, canonical, agents,
		"agents synthesis must match the projects-owned copy")
}

func TestExtensionsUseSameAzdSdk(t *testing.T) {
	t.Parallel()

	projectsVersion := readAzdSdkVersion(t, "../../go.mod")
	agentsVersion := readAzdSdkVersion(
		t,
		filepath.Join(agentsSynthesisPath, "..", "..", "go.mod"),
	)

	assert.Equal(t, projectsVersion, agentsVersion)
}

func readSynthesisFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()

	files := map[string][]byte{}
	err := filepath.WalkDir(
		root,
		func(path string, entry fs.DirEntry, walkErr error) error {
			require.NoError(t, walkErr)
			if entry.IsDir() {
				return nil
			}

			rel, err := filepath.Rel(root, path)
			require.NoError(t, err)
			if !isParityFile(rel) {
				return nil
			}

			//nolint:gosec // repository-controlled parity path
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
			files[filepath.ToSlash(rel)] = data
			return nil
		},
	)
	require.NoError(t, err)
	return files
}

func isParityFile(path string) bool {
	path = filepath.ToSlash(path)
	if strings.HasPrefix(path, "templates/") {
		return true
	}
	return strings.HasSuffix(path, ".go") &&
		!strings.HasSuffix(path, "_test.go")
}

func readAzdSdkVersion(t *testing.T, path string) string {
	t.Helper()

	//nolint:gosec // repository-controlled module path
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	match := regexp.MustCompile(
		`(?m)^\s*github\.com/azure/azure-dev/cli/azd\s+(\S+)`,
	).FindSubmatch(data)
	require.Len(t, match, 2)
	return string(bytes.TrimSpace(match[1]))
}
