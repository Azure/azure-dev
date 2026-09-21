// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
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
		sampleDir := filepath.Join(sourceRepo, filepath.FromSlash(rleGymSamplesPath), sampleName)
		if err := os.MkdirAll(sampleDir, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sampleDir, "sample.txt"), []byte(sampleName), 0600); err != nil {
			t.Fatal(err)
		}
	}
	skillDir := filepath.Join(sourceRepo, filepath.FromSlash(rleSkillsPath), rleGymSkillDirectory)
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("rle skill"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(skillDir, "references", "workflow.md"),
		[]byte("workflow"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	otherSkillDir := filepath.Join(sourceRepo, filepath.FromSlash(rleSkillsPath), "rle-testing")
	if err := os.MkdirAll(otherSkillDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherSkillDir, "SKILL.md"), []byte("testing skill"), 0600); err != nil {
		t.Fatal(err)
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

	catalog, err := loadRleSampleCatalog(sourceRepo, "main", RleSampleCatalogOptions{})
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
	if _, err := os.Stat(filepath.Join(catalog.repoDir, filepath.FromSlash(rleGymSamplesPath))); !os.IsNotExist(err) {
		t.Fatalf("expected sample contents not to be checked out before selection, got err=%v", err)
	}

	sessionDir, err := catalog.Copy("math_rl", "training_env", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "sample.txt")); err != nil {
		t.Fatalf("expected selected sample to be copied: %v", err)
	}
	if _, err := os.Stat(
		filepath.Join(sessionDir, filepath.FromSlash(rleSkillsPath), rleGymSkillDirectory, "SKILL.md"),
	); err != nil {
		t.Fatalf("expected RLE authoring skill to be copied: %v", err)
	}
	if _, err := os.Stat(
		filepath.Join(
			sessionDir,
			filepath.FromSlash(rleSkillsPath),
			rleGymSkillDirectory,
			"references",
			"workflow.md",
		),
	); err != nil {
		t.Fatalf("expected RLE authoring skill references to be copied: %v", err)
	}
	if _, err := os.Stat(
		filepath.Join(sessionDir, filepath.FromSlash(rleSkillsPath), "rle-testing", "SKILL.md"),
	); err != nil {
		t.Fatalf("expected additional RLE project skills to be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(catalog.repoDir, filepath.FromSlash(rleGymSamplesPath), "code_rl")); !os.IsNotExist(err) {
		t.Fatalf("expected unselected sample not to be checked out, got err=%v", err)
	}
}

func TestLoadRleHarnessSampleCatalogCopiesLegacyFlatLayout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	sourceRepo := t.TempDir()
	runTestGit(t, sourceRepo, "init", "--initial-branch=main")
	byohDir := filepath.Join(sourceRepo, filepath.FromSlash(rleHarnessSamplesPath), "byoh")
	if err := os.MkdirAll(filepath.Join(byohDir, "agent"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(byohDir, "agent", "app.py"), []byte("# agent"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(byohDir, "rle"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(byohDir, "rle", "rle.toml"), []byte("[rle]\nname = \"byoh\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hostedAgentDir := filepath.Join(sourceRepo, filepath.FromSlash(rleHarnessSamplesPath), "hosted-agent")
	if err := os.MkdirAll(hostedAgentDir, 0750); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, sourceRepo, "add", ".")
	runTestGit(
		t,
		sourceRepo,
		"-c", "user.name=RLE Tests",
		"-c", "user.email=rle-tests@example.com",
		"commit", "-m", "Add harness samples",
	)

	catalog, err := loadRleHarnessSampleCatalog(sourceRepo, "main", RleSubtypeBYOH, RleSampleCatalogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := catalog.Close(); err != nil {
			t.Errorf("close harness sample catalog: %v", err)
		}
	})
	// A subtype directory holding agent/ and rle/ is the sample itself, so there
	// is no name to offer and nothing for the caller to prompt about.
	if names := catalog.SampleNames(); len(names) != 0 {
		t.Fatalf("expected no named samples for the legacy flat layout, got %v", names)
	}

	sessionDir, err := catalog.Copy("", "my_byoh", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "agent", "app.py")); err != nil {
		t.Fatalf("expected agent/ to be copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "rle", "rle.toml")); err != nil {
		t.Fatalf("expected rle/ to be copied: %v", err)
	}
	if _, err := catalog.Copy("code_repair", "named_byoh", t.TempDir(), false); err == nil {
		t.Fatal("expected a named sample request to fail against the legacy flat layout")
	}
}

func TestLoadRleHarnessSampleCatalogResolvesNamedSamples(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	sourceRepo := t.TempDir()
	runTestGit(t, sourceRepo, "init", "--initial-branch=main")
	byohPath := filepath.Join(sourceRepo, filepath.FromSlash(rleHarnessSamplesPath), "byoh")
	for _, sampleName := range []string{"code_repair", "hidden_sample", "web_nav"} {
		agentDir := filepath.Join(byohPath, sampleName, "agent")
		if err := os.MkdirAll(agentDir, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "app.py"), []byte(sampleName), 0600); err != nil {
			t.Fatal(err)
		}
		rleDir := filepath.Join(byohPath, sampleName, "rle")
		if err := os.MkdirAll(rleDir, 0750); err != nil {
			t.Fatal(err)
		}
		manifest := fmt.Sprintf("[rle]\nname = %q\n", sampleName)
		if err := os.WriteFile(filepath.Join(rleDir, "rle.toml"), []byte(manifest), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalogContents := "[[sample]]\nname = \"hidden_sample\"\nvisible = false\n"
	if err := os.WriteFile(filepath.Join(byohPath, rleSampleCatalogFile), []byte(catalogContents), 0600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, sourceRepo, "add", ".")
	runTestGit(
		t,
		sourceRepo,
		"-c", "user.name=RLE Tests",
		"-c", "user.email=rle-tests@example.com",
		"commit", "-m", "Add named harness samples",
	)

	catalog, err := loadRleHarnessSampleCatalog(sourceRepo, "main", RleSubtypeBYOH, RleSampleCatalogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := catalog.Close(); err != nil {
			t.Errorf("close harness sample catalog: %v", err)
		}
	})
	if !slices.Equal(catalog.SampleNames(), []string{"code_repair", "web_nav"}) {
		t.Fatalf("expected the catalog to hide hidden_sample, got %v", catalog.SampleNames())
	}

	sessionDir, err := catalog.Copy("web_nav", "my_byoh", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- sessionDir is created under t.TempDir by the catalog under test.
	contents, err := os.ReadFile(filepath.Join(sessionDir, "agent", "app.py"))
	if err != nil {
		t.Fatalf("expected agent/ to be copied: %v", err)
	}
	if string(contents) != "web_nav" {
		t.Fatalf("expected the selected sample to be copied, got %q", contents)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "rle", "rle.toml")); err != nil {
		t.Fatalf("expected rle/ to be copied: %v", err)
	}
	if _, err := catalog.Copy("", "unnamed_byoh", t.TempDir(), false); err == nil {
		t.Fatal("expected an unnamed copy to fail when samples are named")
	}
	if _, err := catalog.Copy("hidden_sample", "hidden_byoh", t.TempDir(), false); err == nil {
		t.Fatal("expected a hidden sample to be unavailable")
	}
}

func TestLoadRleHarnessSampleCatalogShowsHiddenSamples(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	sourceRepo := t.TempDir()
	runTestGit(t, sourceRepo, "init", "--initial-branch=main")
	byohPath := filepath.Join(sourceRepo, filepath.FromSlash(rleHarnessSamplesPath), "byoh")
	for _, sampleName := range []string{"code_repair", "hidden_sample"} {
		for _, contentDir := range rleHarnessSampleContentDirs {
			if err := os.MkdirAll(filepath.Join(byohPath, sampleName, contentDir), 0750); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(byohPath, sampleName, contentDir, "marker.txt")
			if err := os.WriteFile(marker, []byte(sampleName), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	catalogContents := "[[sample]]\nname = \"hidden_sample\"\nvisible = false\n"
	if err := os.WriteFile(filepath.Join(byohPath, rleSampleCatalogFile), []byte(catalogContents), 0600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, sourceRepo, "add", ".")
	runTestGit(
		t,
		sourceRepo,
		"-c", "user.name=RLE Tests",
		"-c", "user.email=rle-tests@example.com",
		"commit", "-m", "Add named harness samples",
	)

	catalog, err := loadRleHarnessSampleCatalog(
		sourceRepo,
		"main",
		RleSubtypeBYOH,
		RleSampleCatalogOptions{ShowHiddenSamples: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := catalog.Close(); err != nil {
			t.Errorf("close harness sample catalog: %v", err)
		}
	})
	if !slices.Equal(catalog.SampleNames(), []string{"code_repair", "hidden_sample"}) {
		t.Fatalf("expected hidden samples to be revealed, got %v", catalog.SampleNames())
	}
}

func TestLoadRleHarnessSampleCatalogRejectsUnsupportedSubtype(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	sourceRepo := t.TempDir()
	runTestGit(t, sourceRepo, "init", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(sourceRepo, "README.md"), []byte("samples"), 0600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, sourceRepo, "add", ".")
	runTestGit(
		t,
		sourceRepo,
		"-c", "user.name=RLE Tests",
		"-c", "user.email=rle-tests@example.com",
		"commit", "-m", "Init",
	)

	if _, err := loadRleHarnessSampleCatalog(
		sourceRepo,
		"main",
		RleSubtypeOpenEnv,
		RleSampleCatalogOptions{},
	); err == nil {
		t.Fatal("expected an error for a subtype with no working harness sample")
	}
}

func TestRleSampleCatalogFiltersHiddenSamples(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}

	sourceRepo := t.TempDir()
	runTestGit(t, sourceRepo, "init", "--initial-branch=main")
	for _, sampleName := range []string{"code_rl", "math_rl", "hidden_sample"} {
		sampleDir := filepath.Join(sourceRepo, filepath.FromSlash(rleGymSamplesPath), sampleName)
		if err := os.MkdirAll(sampleDir, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sampleDir, "sample.txt"), []byte(sampleName), 0600); err != nil {
			t.Fatal(err)
		}
	}
	catalogPath := filepath.Join(sourceRepo, filepath.FromSlash(rleGymSamplesPath), rleSampleCatalogFile)
	catalogContents := "[[sample]]\nname = \"hidden_sample\"\nvisible = false\n"
	if err := os.WriteFile(catalogPath, []byte(catalogContents), 0600); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(sourceRepo, filepath.FromSlash(rleSkillsPath), rleGymSkillDirectory)
	if err := os.MkdirAll(skillDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("rle skill"), 0600); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, sourceRepo, "add", ".")
	runTestGit(
		t,
		sourceRepo,
		"-c", "user.name=RLE Tests",
		"-c", "user.email=rle-tests@example.com",
		"commit", "-m", "Add samples with catalog",
	)

	catalog, err := loadRleSampleCatalog(sourceRepo, "main", RleSampleCatalogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := catalog.Close(); err != nil {
			t.Errorf("close sample catalog: %v", err)
		}
	})
	if !slices.Equal(catalog.SampleNames(), []string{"code_rl", "math_rl"}) {
		t.Fatalf("expected hidden sample to be filtered out, got %v", catalog.SampleNames())
	}

	catalogWithHidden, err := loadRleSampleCatalog(sourceRepo, "main", RleSampleCatalogOptions{ShowHiddenSamples: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := catalogWithHidden.Close(); err != nil {
			t.Errorf("close sample catalog: %v", err)
		}
	})
	if !slices.Equal(catalogWithHidden.SampleNames(), []string{"code_rl", "hidden_sample", "math_rl"}) {
		t.Fatalf("expected ShowHiddenSamples to reveal every sample, got %v", catalogWithHidden.SampleNames())
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

func TestCopyRleGymSampleValidatesSkillsBeforeReplacingDestination(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "sample.txt"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	skillSourceDir := t.TempDir()
	destDir := t.TempDir()
	sessionDir := filepath.Join(destDir, "my_environment")
	if err := os.MkdirAll(sessionDir, 0750); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(sessionDir, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := copyRleGymSample(
		sourceDir,
		skillSourceDir,
		"my_environment",
		destDir,
		true,
	)
	if err == nil {
		t.Fatal("expected RLE project skills without the Gym/OpenEnv SKILL.md to fail")
	}
	if _, statErr := os.Stat(sentinel); statErr != nil {
		t.Fatalf("expected destination to remain unchanged after skill lookup failure: %v", statErr)
	}
}
