// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteTaskArtifactJSONRetriesWindowsFileLock(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "task-artifacts.json")
	if err := os.WriteFile(filename, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	locked, err := os.Open(filename) //nolint:gosec // Test-owned file under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	timer := time.AfterFunc(100*time.Millisecond, func() {
		released <- locked.Close()
	})
	defer func() {
		if timer.Stop() {
			if err := locked.Close(); err != nil {
				t.Error(err)
			}
		} else if err := <-released; err != nil {
			t.Error(err)
		}
	}()

	if err := writeTaskArtifactJSON(filename, map[string]string{"status": "ready"}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filename) //nolint:gosec // Test-owned file under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "{\n  \"status\": \"ready\"\n}\n" {
		t.Fatalf("expected the replacement journal after the file lock cleared, got %q", contents)
	}
}
