// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyDirectorySkipsGitMetadata(t *testing.T) {
	sourceDir := t.TempDir()
	destDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sourceDir, ".git"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, ".git", "config"), []byte("[remote]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := copyDirectory(sourceDir, destDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destDir, "Dockerfile")); err != nil {
		t.Fatalf("expected Dockerfile to be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected .git metadata to be skipped, got err=%v", err)
	}
}

func TestCopyDirectorySkipsSymlinksAndPreservesFileMode(t *testing.T) {
	sourceDir := t.TempDir()
	destDir := t.TempDir()
	scriptPath := filepath.Join(sourceDir, "run.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0700); err != nil { //nolint:gosec // test fixture needs an executable bit to verify mode preservation.
		t.Fatal(err)
	}
	if err := os.Symlink(scriptPath, filepath.Join(sourceDir, "linked-run.sh")); err != nil {
		t.Skipf("symlinks are not available in this environment: %v", err)
	}

	if err := copyDirectory(sourceDir, destDir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(destDir, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		t.Fatalf("expected executable bit to be preserved, got %v", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(destDir, "linked-run.sh")); !os.IsNotExist(err) {
		t.Fatalf("expected symlink to be skipped, got err=%v", err)
	}
}

func TestCopyDirectoryRejectsFileSource(t *testing.T) {
	sourceFile := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(sourceFile, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyDirectory(sourceFile, t.TempDir()); err == nil {
		t.Fatal("expected file source path to be rejected")
	}
}

func TestCheckoutOpenEnvEchoSampleRejectsInvalidNameBeforeChangingDestination(t *testing.T) {
	destDir := t.TempDir()
	sentinel := filepath.Join(destDir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := CheckoutOpenEnvEchoSample("../bad", destDir, true); err == nil {
		t.Fatal("expected invalid environment name to be rejected")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("expected destination to be unchanged: %v", err)
	}
}
