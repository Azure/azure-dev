// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.rle/internal/project"
)

func TestSkillInstallCommandInstallsIntoCurrentProject(t *testing.T) {
	originalInstall := installRleSkillsFunc
	originalFindRoot := findRleProjectRootFunc
	t.Cleanup(func() {
		installRleSkillsFunc = originalInstall
		findRleProjectRootFunc = originalFindRoot
	})
	projectRoot := t.TempDir()
	findRleProjectRootFunc = func() (string, error) {
		return projectRoot, nil
	}
	var installDest string
	installRleSkillsFunc = func(dest string) ([]string, error) {
		installDest = dest
		return []string{"rle-gym-openenv", "rle-testing"}, nil
	}

	command := newSkillInstallCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if installDest != projectRoot {
		t.Fatalf("expected skills to be installed into the current project, got %q", installDest)
	}
	for _, expected := range []string{".agents/skills", "rle-gym-openenv", "rle-testing"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output.String())
		}
	}
}

func TestSkillInstallCommandReturnsInstallError(t *testing.T) {
	originalInstall := installRleSkillsFunc
	originalFindRoot := findRleProjectRootFunc
	t.Cleanup(func() {
		installRleSkillsFunc = originalInstall
		findRleProjectRootFunc = originalFindRoot
	})
	findRleProjectRootFunc = func() (string, error) {
		return t.TempDir(), nil
	}
	expectedErr := errors.New("install failed")
	installRleSkillsFunc = func(string) ([]string, error) {
		return nil, expectedErr
	}

	err := newSkillInstallCommand().Execute()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected install error, got %v", err)
	}
}

func TestFindRleProjectRootFromSubdirectory(t *testing.T) {
	projectRoot := t.TempDir()
	config := project.RleConfig{
		Rle: project.RleManifest{
			Name:    "skill_test",
			Version: "1.0.0",
			Type:    project.RleTypeGym,
			Subtype: project.RleSubtypeOpenEnv,
		},
	}
	if err := project.WriteRleConfig(projectRoot, config); err != nil {
		t.Fatal(err)
	}
	subdirectory := filepath.Join(projectRoot, "server", "nested")
	if err := os.MkdirAll(subdirectory, 0750); err != nil {
		t.Fatal(err)
	}
	actualRoot, err := findRleProjectRootFrom(subdirectory)
	if err != nil {
		t.Fatal(err)
	}
	if actualRoot != projectRoot {
		t.Fatalf("expected project root %q, got %q", projectRoot, actualRoot)
	}
}
