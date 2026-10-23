// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHarborTask(t *testing.T, taskToml string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"task.toml":              taskToml,
		"instruction.md":         "do it",
		"environment/Dockerfile": "FROM scratch\n",
		"tests/test.sh":          "#!/bin/sh\n",
	}
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadHarborTaskComputesStableImageTag(t *testing.T) {
	dir := writeHarborTask(t, "[environment]\ncpus = 1\n")
	first, err := LoadHarborTask(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadHarborTask(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.ImageTag != second.ImageTag || !strings.HasPrefix(first.ImageTag, "task-") {
		t.Fatalf("unstable or malformed tag: %q vs %q", first.ImageTag, second.ImageTag)
	}
	if err := os.WriteFile(filepath.Join(dir, "environment", "Dockerfile"), []byte("FROM alpine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := LoadHarborTask(dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ImageTag == first.ImageTag {
		t.Fatal("expected tag to change with environment content")
	}
}

func TestLoadHarborTaskRejectsMissingFile(t *testing.T) {
	dir := writeHarborTask(t, "")
	if err := os.Remove(filepath.Join(dir, "tests", "test.sh")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHarborTask(dir); err == nil || !strings.Contains(err.Error(), "tests/test.sh") {
		t.Fatalf("expected missing test.sh error, got %v", err)
	}
}

func TestLoadHarborTaskRejectsUnsupported(t *testing.T) {
	for name, toml := range map[string]string{
		"gpu":   "[environment]\ngpus = 1\n",
		"steps": "[[steps]]\nname = \"a\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadHarborTask(writeHarborTask(t, toml)); err == nil ||
				!strings.Contains(err.Error(), "not supported") {
				t.Fatalf("expected unsupported error, got %v", err)
			}
		})
	}
}
