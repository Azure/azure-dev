// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeBundledRleSdkWritesWheel(t *testing.T) {
	sessionDir := t.TempDir()

	wheelPath, err := materializeBundledRleSdk(sessionDir)
	if err != nil {
		t.Fatal(err)
	}

	expectedPath := filepath.Join(sessionDir, rleManagedDir, rleDepsDir, bundledRleSdkWheelName)
	if wheelPath != expectedPath {
		t.Fatalf("expected wheel path %q, got %q", expectedPath, wheelPath)
	}

	data, err := os.ReadFile(wheelPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected bundled wheel to be non-empty")
	}
}
