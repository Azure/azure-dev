// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build mage

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ----------------------------------------------------------------------------
// resolveCoverageFile — pure filesystem; covers the env-override + default
// fallback contract used by Coverage.Diff / Coverage.PR.
// ----------------------------------------------------------------------------

func TestResolveCoverageFile_EnvOverride_Exists(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "override.out")
	if err := os.WriteFile(envFile, []byte("mode: set\n"), 0o644); err != nil {
		t.Fatalf("seed env file: %v", err)
	}
	defaultFile := filepath.Join(dir, "default.out") // intentionally not created

	got, err := resolveCoverageFile(envFile, defaultFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != envFile {
		t.Fatalf("want %q, got %q", envFile, got)
	}
}

func TestResolveCoverageFile_EnvOverride_Missing(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "missing.out") // not created
	defaultFile := filepath.Join(dir, "default.out")
	if err := os.WriteFile(defaultFile, []byte("mode: set\n"), 0o644); err != nil {
		t.Fatalf("seed default file: %v", err)
	}

	// Env override takes precedence even when missing — must error rather
	// than silently falling back to the default (callers explicitly opted
	// in to a specific file).
	_, err := resolveCoverageFile(envFile, defaultFile)
	if err == nil {
		t.Fatal("expected error for missing env-override file, got nil")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("expected 'file not found' error, got: %v", err)
	}
}

func TestResolveCoverageFile_DefaultExists(t *testing.T) {
	dir := t.TempDir()
	defaultFile := filepath.Join(dir, "default.out")
	if err := os.WriteFile(defaultFile, []byte("mode: set\n"), 0o644); err != nil {
		t.Fatalf("seed default file: %v", err)
	}

	got, err := resolveCoverageFile("", defaultFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != defaultFile {
		t.Fatalf("want %q, got %q", defaultFile, got)
	}
}

func TestResolveCoverageFile_DefaultMissing(t *testing.T) {
	dir := t.TempDir()
	defaultFile := filepath.Join(dir, "missing.out") // not created

	_, err := resolveCoverageFile("", defaultFile)
	if err == nil {
		t.Fatal("expected error for missing default file, got nil")
	}
	if !strings.Contains(err.Error(), "file not found") {
		t.Fatalf("expected 'file not found' error, got: %v", err)
	}
}

// ----------------------------------------------------------------------------
// resolveBaselineFile — env override + default; the CI-download fallback
// is intentionally not exercised here (requires az login).
// ----------------------------------------------------------------------------

func TestResolveBaselineFile_EnvOverride_Exists(t *testing.T) {
	t.Setenv("COVERAGE_BASELINE", "")
	dir := t.TempDir()
	envFile := filepath.Join(dir, "baseline.out")
	if err := os.WriteFile(envFile, []byte("mode: set\n"), 0o644); err != nil {
		t.Fatalf("seed env baseline: %v", err)
	}
	t.Setenv("COVERAGE_BASELINE", envFile)

	got, err := resolveBaselineFile(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != envFile {
		t.Fatalf("want %q, got %q", envFile, got)
	}
}

func TestResolveBaselineFile_EnvOverride_Missing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COVERAGE_BASELINE", filepath.Join(dir, "missing.out"))

	_, err := resolveBaselineFile(dir)
	if err == nil {
		t.Fatal("expected error for missing baseline override, got nil")
	}
	if !strings.Contains(err.Error(), "baseline file not found") {
		t.Fatalf("expected 'baseline file not found' error, got: %v", err)
	}
}

func TestResolveBaselineFile_DefaultExists(t *testing.T) {
	t.Setenv("COVERAGE_BASELINE", "")
	dir := t.TempDir()
	defaultBaseline := filepath.Join(dir, "cover-ci-combined.out")
	if err := os.WriteFile(defaultBaseline, []byte("mode: set\n"), 0o644); err != nil {
		t.Fatalf("seed default baseline: %v", err)
	}

	got, err := resolveBaselineFile(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != defaultBaseline {
		t.Fatalf("want %q, got %q", defaultBaseline, got)
	}
}

// ----------------------------------------------------------------------------
// resolveChangedFilesForDiff — uses real git in temp repos so we exercise
// the actual git invocations the function relies on.
// ----------------------------------------------------------------------------

// initTempRepo seeds a fresh git repo in t.TempDir() with one commit on main.
// Returns the repo dir and the initial commit SHA.
func initTempRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH; skipping git-backed test")
	}
	dir := t.TempDir()
	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Repo-local identity so CI runners without global config still work.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	mustGit("init", "-b", "main")
	mustGit("config", "user.email", "test@example.com")
	mustGit("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	mustGit("add", ".")
	mustGit("commit", "-m", "seed")
	return dir
}

func TestResolveChangedFilesForDiff_OnMain_NonStrict(t *testing.T) {
	dir := initTempRepo(t)

	got, err := resolveChangedFilesForDiff(dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty path on main, got %q", got)
	}
}

func TestResolveChangedFilesForDiff_OnMain_Strict(t *testing.T) {
	dir := initTempRepo(t)

	_, err := resolveChangedFilesForDiff(dir, true)
	if err == nil {
		t.Fatal("expected error in strict mode on main, got nil")
	}
	if !strings.Contains(err.Error(), "on main branch") {
		t.Fatalf("expected 'on main branch' error, got: %v", err)
	}
}

func TestResolveChangedFilesForDiff_DetachedHEAD_NonStrict(t *testing.T) {
	dir := initTempRepo(t)

	// Detach HEAD by checking out the commit SHA directly.
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	shaOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	sha := strings.TrimSpace(string(shaOut))
	cmd = exec.Command("git", "checkout", "--detach", sha)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout --detach: %v\n%s", err, out)
	}

	got, err := resolveChangedFilesForDiff(dir, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty path on detached HEAD, got %q", got)
	}
}

// Validates the happy path: a feature branch with diffs vs origin/main
// returns a file containing the changed-file list.
//
// We simulate `origin/main` by creating a bare upstream repo, pushing main to
// it, then branching locally and diffing against origin/main.
func TestResolveChangedFilesForDiff_FeatureBranch_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}

	// Upstream "remote" — bare repo to push to.
	upstream := t.TempDir()
	cmd := exec.Command("git", "init", "--bare", "-b", "main", upstream)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}

	// Working repo with origin pointing at upstream.
	work := initTempRepo(t)
	mustGit := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = work
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	mustGit("remote", "add", "origin", upstream)
	mustGit("push", "-u", "origin", "main")

	// Branch off, add a Go file (per-package gate cares about Go files).
	mustGit("checkout", "-b", "feature/test")
	pkgDir := filepath.Join(work, "pkg", "foo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "foo.go"), []byte("package foo\n"), 0o644); err != nil {
		t.Fatalf("write foo.go: %v", err)
	}
	mustGit("add", ".")
	mustGit("commit", "-m", "add foo")

	got, err := resolveChangedFilesForDiff(work, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == "" {
		t.Fatal("expected non-empty path on feature branch with diff")
	}
	defer os.Remove(got)

	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read changed-files output: %v", err)
	}
	if !strings.Contains(string(body), filepath.ToSlash(filepath.Join("pkg", "foo", "foo.go"))) {
		t.Fatalf("expected changed-files output to include pkg/foo/foo.go, got:\n%s", body)
	}

	got2, err := resolveChangedFilesForDiff(work, false)
	if err != nil {
		t.Fatalf("second resolve unexpected error: %v", err)
	}
	defer os.Remove(got2)
	if got == got2 {
		t.Fatalf("expected two resolves to produce distinct paths, both got %q", got)
	}
}
