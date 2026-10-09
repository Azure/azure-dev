// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveFileRefs_ConfinedPaths(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project with spaces")
	writeFile(t, root, "nested/agent.json", `{"name":"inside"}`)
	writeFile(t, parent, "outside.yaml", "name: private_fixture_marker\n")
	writeFile(t, parent, "project with spaces sibling/agent.yaml", "name: outside\n")
	tests := []struct {
		name string
		ref  string
		ok   bool
	}{
		{"relative", "./nested/agent.json", true},
		{"absolute", filepath.Join(root, "nested", "agent.json"), true},
		{"absolute slash form", filepath.ToSlash(filepath.Join(root, "nested", "agent.json")), true},
		{"cleaned in root", "./nested/../nested/agent.json", true},
		{"parent traversal", "../outside.yaml", false},
		{"absolute outside", filepath.Join(parent, "outside.yaml"), false},
		{"sibling prefix", filepath.Join(parent, "project with spaces sibling", "agent.yaml"), false},
		{"directory", "./nested", false},
		{"missing", "./missing.yaml", false},
		{"windows separator", `.\nested\agent.json`, runtime.GOOS == "windows"},
	}
	if runtime.GOOS == "windows" {
		volume := "Z:"
		if filepath.VolumeName(root) == volume {
			volume = "Y:"
		}
		tests = append(tests,
			struct {
				name string
				ref  string
				ok   bool
			}{"different volume", volume + `\outside.yaml`, false},
			struct {
				name string
				ref  string
				ok   bool
			}{"reserved device", "NUL", false},
		)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveFileRefs(map[string]any{refKey: tt.ref}, root, WithProjectRootConfinement())
			if tt.ok {
				require.NoError(t, err)
				assert.Equal(t, map[string]any{"name": "inside"}, got)
			} else {
				requireFileRefError(t, err, "cannot read")
				assert.NotContains(t, err.Error(), "private_fixture_marker")
				assert.Nil(t, got)
			}
		})
	}

	for _, ref := range []string{"../outside.yaml", filepath.Join(parent, "outside.yaml")} {
		got, err := ResolveFileRefs(map[string]any{refKey: ref}, root)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"name": "private_fixture_marker"}, got)
	}
}

func TestResolveFileRefs_ConfinedExactlyOneObject(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
		ok      bool
	}{
		{"YAML", "name: inside\n", true},
		{"JSON", `{"name":"inside"}`, true},
		{"empty object", "{}", true},
		{"multiple YAML documents", "name: inside\n---\nname: other\n", false},
		{"empty trailing document", "name: inside\n---\n", false},
		{"adjacent JSON objects", `{"name":"inside"} {"name":"other"}`, false},
		{"trailing malformed content", "name: inside\n---\n[", false},
		{"array", "[inside]", false},
		{"scalar", "inside", false},
		{"null", "null", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "agent.yaml", tt.content)
			got, err := ResolveFileRefs(map[string]any{refKey: "./agent.yaml"}, root, WithProjectRootConfinement())
			if tt.ok {
				require.NoError(t, err)
				want := map[string]any{"name": "inside"}
				if tt.content == "{}" {
					want = map[string]any{}
				}
				assert.Equal(t, want, got)
			} else {
				requireFileRefError(t, err, "agent.yaml")
				assert.Nil(t, got)
			}
		})
	}
}

func TestResolveFileRefs_ConfinedNestedAndOverlay(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project with spaces")
	writeFile(t, root, "agents/base.yaml", `
name: base
project: ../src
env: {A: first, B: second}
items: [first, second]
skill:
  $ref: ../skills/skill.json
`)
	writeFile(t, root, "skills/skill.json",
		`{"instructions":"../prompts/skill.md","source":"./rubric.json","unknown":"./unchanged.json"}`)
	writeFile(t, root, "skills/overlay.yaml", "name: overlay\ninstructions: ../prompts/overlay.txt\n")
	cfg := map[string]any{
		refKey: "./agents/base.yaml",
		"name": "override",
		"env":  map[string]any{"C": "third"},
		"items": []any{
			map[string]any{refKey: "./skills/overlay.yaml"},
		},
		"instructions": "./inline.md",
	}
	want := map[string]any{
		"name": "override", "project": "src",
		"env":   map[string]any{"C": "third"},
		"items": []any{map[string]any{"name": "overlay", "instructions": "prompts/overlay.txt"}},
		"skill": map[string]any{
			"instructions": "prompts/skill.md", "source": "skills/rubric.json", "unknown": "./unchanged.json",
		},
		"instructions": "./inline.md",
	}
	got, err := ResolveFileRefs(cfg, root, WithProjectRootConfinement(), WithPathKeys("source"))
	require.NoError(t, err)
	assert.Equal(t, want, got)

	writeFile(t, root, "agents/escape.yaml", "child:\n  $ref: ../../outside.yaml\n")
	writeFile(t, filepath.Dir(root), "outside.yaml", "private_fixture_marker: [\n")
	_, err = ResolveFileRefs(map[string]any{refKey: "./agents/escape.yaml"}, root, WithProjectRootConfinement())
	requireFileRefError(t, err, "outside the project root")
	assert.NotContains(t, err.Error(), "private_fixture_marker")
}

func TestResolveFileRefs_ConfinedRelativeRoot(t *testing.T) {
	parent := t.TempDir()
	writeFile(t, parent, "project with spaces/agent.yaml", "name: inside\n")
	t.Chdir(parent)
	got, err := ResolveFileRefs(map[string]any{refKey: "./agent.yaml"},
		"project with spaces", WithProjectRootConfinement())
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "inside"}, got)
}

func TestResolveFileRefs_ConfinedInvalidRoot(t *testing.T) {
	for _, root := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		_, err := ResolveFileRefs(map[string]any{refKey: "./agent.yaml"}, root, WithProjectRootConfinement())
		requireFileRefError(t, err, "project root")
	}
}

func TestResolveFileRefs_ConfinedCycleAndDepth(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "cycle.yaml", "$ref: ./cycle.yaml\n")
	_, err := ResolveFileRefs(map[string]any{refKey: "./cycle.yaml"}, root, WithProjectRootConfinement())
	requireFileRefError(t, err, "cyclic")

	for i := range maxRefDepth {
		writeFile(t, root, fmt.Sprintf("%d.yaml", i), fmt.Sprintf("$ref: ./%d.yaml\n", i+1))
	}
	writeFile(t, root, fmt.Sprintf("%d.yaml", maxRefDepth), "name: deepest\n")
	got, err := ResolveFileRefs(map[string]any{refKey: "./1.yaml"}, root, WithProjectRootConfinement())
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "deepest"}, got)
	_, err = ResolveFileRefs(map[string]any{refKey: "./0.yaml"}, root, WithProjectRootConfinement())
	requireFileRefError(t, err, "nesting exceeds")
}

func TestResolveFileRefs_URLCredentialsNotDisclosed(t *testing.T) {
	for _, opts := range [][]ResolveOption{nil, {WithProjectRootConfinement()}} {
		// #nosec G101 -- Fake credentials test error non-disclosure.
		_, err := ResolveFileRefs(map[string]any{
			refKey: "https://private_user:private_password@example.com/agent.yaml?sig=private_signature#private_fragment",
		}, t.TempDir(), opts...)
		requireFileRefError(t, err, "remote includes are not supported")
		for _, secret := range []string{"private_user", "private_password", "private_signature", "private_fragment"} {
			assert.NotContains(t, err.Error(), secret)
		}
	}
}

func createRefSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		require.NoError(t, err)
	}
}

func TestResolveFileRefs_ConfinedSymlinks(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	writeFile(t, root, "nested/agent.yaml", "name: inside\nchild:\n  $ref: ./child.json\n")
	writeFile(t, root, "nested/child.json", `{"name":"child"}`)
	writeFile(t, parent, "outside.yaml", "name: private_fixture_marker\n")
	createRefSymlink(t, "nested", filepath.Join(root, "alias"))
	_, err := ResolveFileRefs(map[string]any{refKey: "./alias"}, root, WithProjectRootConfinement())
	requireFileRefError(t, err, "must be a regular file")
	got, err := ResolveFileRefs(map[string]any{refKey: "./alias/agent.yaml"}, root, WithProjectRootConfinement())
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "inside", "child": map[string]any{"name": "child"}}, got)

	createRefSymlink(t, "nested/child.json", filepath.Join(root, "inside.json"))
	got, err = ResolveFileRefs(map[string]any{refKey: "./inside.json"}, root, WithProjectRootConfinement())
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"name": "child"}, got)

	for _, tt := range []struct {
		name   string
		target string
	}{
		{"relative-outside", "../outside.yaml"},
		{"absolute-outside", filepath.Join(parent, "outside.yaml")},
		{"absolute-inside", filepath.Join(root, "nested", "child.json")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			createRefSymlink(t, tt.target, filepath.Join(root, tt.name))
			got, err := ResolveFileRefs(map[string]any{refKey: "./" + tt.name}, root, WithProjectRootConfinement())
			requireFileRefError(t, err, "cannot read")
			assert.NotContains(t, err.Error(), "private_fixture_marker")
			assert.Nil(t, got)
			got, err = ResolveFileRefs(map[string]any{refKey: "./" + tt.name}, root)
			require.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestReadRefFile_ConfinedSymlinkReplacement(t *testing.T) {
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "project")
	writeFile(t, rootPath, "inside.yaml", "name: inside\n")
	writeFile(t, parent, "outside.yaml", "name: private_fixture_marker\n")
	link := filepath.Join(rootPath, "changing.yaml")
	createRefSymlink(t, "inside.yaml", link)
	root, err := os.OpenRoot(rootPath)
	require.NoError(t, err)
	defer root.Close()
	data, err := readRefFile(link, root)
	require.NoError(t, err)
	require.Equal(t, "name: inside\n", string(data))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var swapErr error
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, target := range []string{"../outside.yaml", "inside.yaml"} {
				if err := os.Remove(link); err != nil {
					swapErr = err
					return
				}
				if err := os.Symlink(target, link); err != nil {
					swapErr = err
					return
				}
			}
		}
	})
	for range 500 {
		data, err := readRefFile(link, root)
		if err == nil {
			assert.Equal(t, "name: inside\n", string(data))
		}
		assert.NotContains(t, string(data), "private_fixture_marker")
	}
	close(stop)
	wg.Wait()
	require.NoError(t, swapErr)
}
