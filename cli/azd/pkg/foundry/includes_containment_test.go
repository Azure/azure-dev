// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveFileRefs_ProjectContainment(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, root, "connections/base.yaml", "name: contained\n")
	writeFile(t, outside, "outside.yaml", "name: outside\n")
	writeFile(t, root, "shared/base.yaml", "name: shared\n")
	writeFile(t, root, "connections/nested/outer.yaml", `
name: outer
included:
  $ref: ../../shared/base.yaml
`)

	tests := []struct {
		name string
		ref  string
		want map[string]any
	}{
		{
			name: "relative reference",
			ref:  "./connections/base.yaml",
			want: map[string]any{"name": "contained"},
		},
		{
			name: "absolute reference inside project",
			ref:  filepath.Join(root, "connections", "base.yaml"),
			want: map[string]any{"name": "contained"},
		},
		{
			name: "nested parent traversal remains inside project",
			ref:  "./connections/nested/outer.yaml",
			want: map[string]any{
				"name":     "outer",
				"included": map[string]any{"name": "shared"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveFileRefs(
				map[string]any{refKey: tt.ref},
				root,
				WithProjectContainment(),
			)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	for _, ref := range []string{
		"../outside.yaml",
		filepath.Join(outside, "outside.yaml"),
	} {
		t.Run(ref, func(t *testing.T) {
			_, err := ResolveFileRefs(
				map[string]any{refKey: ref},
				root,
				WithProjectContainment(),
			)
			requireFileRefError(t, err, "inside the project")
			assert.NotContains(t, err.Error(), outside)
		})
	}
}

func TestResolveFileRefs_ProjectContainmentDoesNotChangeDefault(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "legacy.yaml", "name: legacy\n")

	got, err := ResolveFileRefs(
		map[string]any{refKey: filepath.Join(outside, "legacy.yaml")},
		root,
	)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "legacy"}, got)
}

func TestResolveFileRefs_ProjectContainmentRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "secret.yaml", "name: private-symlink-marker\n")
	link := filepath.Join(root, "escape.yaml")
	if err := os.Symlink(filepath.Join(outside, "secret.yaml"), link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	_, err := ResolveFileRefs(
		map[string]any{refKey: "./escape.yaml"},
		root,
		WithProjectContainment(),
	)
	requireFileRefError(t, err, "outside the project")
	assert.NotContains(t, err.Error(), outside)
	assert.NotContains(t, err.Error(), "private-symlink-marker")
}

func TestResolveFileRefs_ProjectContainmentAllowsInternalFileSymlink(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "targets/outer.yaml", `
included:
  $ref: ./inner.yaml
`)
	writeFile(t, root, "targets/inner.yaml", "name: target-directory\n")
	writeFile(t, root, "links/inner.yaml", "name: symlink-directory\n")
	link := filepath.Join(root, "links", "outer.yaml")
	if err := os.Symlink(filepath.Join(root, "targets", "outer.yaml"), link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	got, err := ResolveFileRefs(
		map[string]any{refKey: "./links/outer.yaml"},
		root,
		WithProjectContainment(),
	)
	require.NoError(t, err)
	assert.Equal(
		t,
		map[string]any{
			"included": map[string]any{"name": "symlink-directory"},
		},
		got,
	)
}

func TestResolveFileRefs_ProjectContainmentDetectsSymlinkCycle(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cycle.yaml", "included:\n  $ref: ./alias.yaml\n")
	if err := os.Symlink(filepath.Join(root, "cycle.yaml"), filepath.Join(root, "alias.yaml")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	_, err := ResolveFileRefs(
		map[string]any{refKey: "./cycle.yaml"},
		root,
		WithProjectContainment(),
	)
	requireFileRefError(t, err, "cyclic")
	assert.NotContains(t, err.Error(), root)
}
