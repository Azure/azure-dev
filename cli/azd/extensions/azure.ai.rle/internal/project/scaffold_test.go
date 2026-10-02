// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
	// The fixture needs an executable bit to verify mode preservation.
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0700); err != nil { //nolint:gosec
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

func TestRleSampleCatalogUsesSparseCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	sourceRepo := t.TempDir()
	runTestGit(t, sourceRepo, "init", "--initial-branch=main")
	for _, sampleName := range []string{"code_rl", "math_rl"} {
		sampleDir := filepath.Join(sourceRepo, "envs", sampleName)
		if err := os.MkdirAll(sampleDir, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sampleDir, "sample.txt"), []byte(sampleName), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sourceRepo, "README.md"), []byte("samples"), 0600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, sourceRepo, "add", ".")
	runTestGit(
		t,
		sourceRepo,
		"-c", "user.name=RLE Tests",
		"-c", "user.email=rle-tests@example.com",
		"commit", "-m", "Add samples",
	)

	catalog, err := loadRleSampleCatalog(sourceRepo, "main")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := catalog.Close(); err != nil {
			t.Errorf("close sample catalog: %v", err)
		}
	})
	if !slices.Equal(catalog.SampleNames(), []string{"code_rl", "math_rl"}) {
		t.Fatalf("expected sorted sample names, got %v", catalog.SampleNames())
	}
	if _, err := os.Stat(filepath.Join(catalog.repoDir, "envs")); !os.IsNotExist(err) {
		t.Fatalf("expected sample contents not to be checked out before selection, got err=%v", err)
	}

	sessionDir, err := catalog.Copy("math_rl", "training_env", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "sample.txt")); err != nil {
		t.Fatalf("expected selected sample to be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(catalog.repoDir, "envs", "code_rl")); !os.IsNotExist(err) {
		t.Fatalf("expected unselected sample not to be checked out, got err=%v", err)
	}
}

func TestCopyRleSampleRenamesDestination(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "sample.txt"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	destDir := t.TempDir()
	sessionDir, err := copyRleSample(sourceDir, "my_environment", destDir, false)
	if err != nil {
		t.Fatal(err)
	}
	if sessionDir != filepath.Join(destDir, "my_environment") {
		t.Fatalf("expected renamed destination, got %q", sessionDir)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "sample.txt")); err != nil {
		t.Fatalf("expected sample file in renamed destination: %v", err)
	}
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...) //nolint:gosec
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func TestCopyRleSampleValidatesSourceBeforeReplacingDestination(t *testing.T) {
	destDir := t.TempDir()
	sessionDir := filepath.Join(destDir, "my_environment")
	if err := os.MkdirAll(sessionDir, 0750); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(sessionDir, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := copyRleSample(filepath.Join(t.TempDir(), "missing"), "my_environment", destDir, true)
	if err == nil {
		t.Fatal("expected missing RLE sample to fail")
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Fatalf("expected destination to remain unchanged after sample lookup failure: %v", statErr)
	}
}
