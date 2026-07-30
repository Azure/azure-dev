// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestReadSkillBundleFiles_ReadsAllNestedFiles verifies that every file in a
// skill bundle — SKILL.md plus references/, assets/, and scripts/ subfolders —
// is picked up, not just SKILL.md. This is the core of the reported bug: only
// SKILL.md was ever read/uploaded, silently dropping everything else.
func TestReadSkillBundleFiles_ReadsAllNestedFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"SKILL.md":            "---\nname: s\ndescription: d\n---\nbody",
		"references/tone.md":  "tone guidance",
		"assets/logo.svg":     "<svg/>",
		"scripts/analysis.py": "print('hi')",
	}
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	got, err := readSkillBundleFiles(dir)
	if err != nil {
		t.Fatalf("readSkillBundleFiles: %v", err)
	}

	if len(got) != len(files) {
		t.Fatalf("got %d files, want %d: %v", len(got), len(files), keysOf(got))
	}
	for rel, want := range files {
		content, ok := got[rel]
		if !ok {
			t.Errorf("missing bundle file %q in result", rel)
			continue
		}
		if string(content) != want {
			t.Errorf("file %q content: got %q, want %q", rel, content, want)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestReadSkillBundleFiles_EmptyDirErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := readSkillBundleFiles(dir); err == nil {
		t.Fatal("expected an error for an empty bundle directory")
	}
}
