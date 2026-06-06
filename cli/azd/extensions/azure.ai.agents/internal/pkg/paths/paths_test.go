// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package paths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJoin(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	tests := []struct {
		name string
		rel  string
		want string
	}{
		{"nested path", "src/agent", filepath.Join(root, "src", "agent", "agent.yaml")},
		{"dot segment normalizes", "./src/agent", filepath.Join(root, "src", "agent", "agent.yaml")},
		{"windows separator normalizes", `src\agent`, filepath.Join(root, "src", "agent", "agent.yaml")},
		{"spaces are preserved", " src ", filepath.Join(root, " src ", "agent.yaml")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Join(root, tt.rel, "agent.yaml")

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestJoinRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	tests := []struct {
		name string
		rel  string
	}{
		{"empty", ""},
		{"whitespace", " "},
		{"dot root", "."},
		{"parent traversal", "../outside"},
		{"nested traversal", "src/../outside"},
		{"windows traversal", `src\..\outside`},
		{"absolute", filepath.Join(root, "outside")},
		{"windows drive", `C:\outside`},
		{"unc", `\\server\share`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Join(root, tt.rel, "agent.yaml")

			require.Error(t, err)
		})
	}
}

func TestJoinAllowRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	for _, rel := range []string{"", "."} {
		got, err := JoinAllowRoot(root, rel, "agent.yaml")

		require.NoError(t, err)
		require.Equal(t, filepath.Join(root, "agent.yaml"), got)
	}
}

func TestJoinAllowRootRejectsSymlinkEscapingRoot(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(root, 0o750))
	require.NoError(t, os.MkdirAll(outside, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "agent.yaml"), []byte("outside"), 0o600))

	createSymlinkOrSkip(t, outside, filepath.Join(root, "svc"))

	_, err := JoinAllowRoot(root, "svc", "agent.yaml")

	require.Error(t, err)
}

func TestJoinAllowRootAllowsSymlinkWithinRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	require.NoError(t, os.MkdirAll(target, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(target, "agent.yaml"), []byte("inside"), 0o600))

	createSymlinkOrSkip(t, target, filepath.Join(root, "svc"))

	got, err := JoinAllowRoot(root, "svc", "agent.yaml")

	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "svc", "agent.yaml"), got)
}

func TestJoinAllowRootAllowsMissingLeafUnderRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "svc"), 0o750))

	got, err := JoinAllowRoot(root, "svc", "README.md")

	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "svc", "README.md"), got)
}

func createSymlinkOrSkip(t *testing.T, oldname, newname string) {
	t.Helper()

	if err := os.Symlink(oldname, newname); err != nil {
		if errors.Is(err, os.ErrPermission) || os.IsPermission(err) ||
			strings.Contains(strings.ToLower(err.Error()), "privilege") {
			t.Skipf("symlink creation not permitted: %v", err)
		}
		t.Fatalf("create symlink: %v", err)
	}
}
