// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareDockerBuildFindsRootDockerfile(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}

	source, dockerfile, cleanup, err := PrepareDockerBuild(BuildOptions{Source: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		t.Fatal("expected no cleanup for existing source")
	}
	if source != tempDir {
		t.Fatalf("expected source %q, got %q", tempDir, source)
	}
	if dockerfile != filepath.Join(tempDir, "Dockerfile") {
		t.Fatalf("expected root Dockerfile, got %q", dockerfile)
	}
}

func TestPrepareDockerBuildFindsServerDockerfile(t *testing.T) {
	tempDir := t.TempDir()
	serverDir := filepath.Join(tempDir, "server")
	if err := os.MkdirAll(serverDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serverDir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}

	source, dockerfile, cleanup, err := PrepareDockerBuild(BuildOptions{Source: tempDir})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		t.Fatal("expected no cleanup for existing source")
	}
	if source != tempDir {
		t.Fatalf("expected source %q, got %q", tempDir, source)
	}
	if dockerfile != filepath.Join(serverDir, "Dockerfile") {
		t.Fatalf("expected server Dockerfile, got %q", dockerfile)
	}
}

func TestPrepareDockerBuildUsesDockerfileOption(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	dockerDir := filepath.Join(tempDir, "docker")
	if err := os.MkdirAll(dockerDir, 0750); err != nil {
		t.Fatal(err)
	}
	customPath := filepath.Join(dockerDir, "custom.Dockerfile")
	if err := os.WriteFile(customPath, []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, dockerfile, cleanup, err := PrepareDockerBuild(BuildOptions{
		Source:     tempDir,
		Dockerfile: "docker/custom.Dockerfile",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		t.Fatal("expected no cleanup for existing source")
	}
	if dockerfile != customPath {
		t.Fatalf("expected explicit Dockerfile, got %q", dockerfile)
	}
}

func TestIsAcrImageReference(t *testing.T) {
	if !IsAcrImageReference("myregistry.azurecr.io/echo_env:latest") {
		t.Fatal("expected ACR image reference")
	}
	if IsAcrImageReference("echo_env:latest") {
		t.Fatal("did not expect local image tag to be treated as ACR")
	}
}

func TestPrepareDockerBuildRejectsDockerfileEscapes(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dockerfile := range []string{
		filepath.Join("..", filepath.Base(outsideDir), "Dockerfile"),
		filepath.Join(outsideDir, "Dockerfile"),
	} {
		if _, _, _, err := PrepareDockerBuild(BuildOptions{
			Source:     tempDir,
			Dockerfile: dockerfile,
		}); err == nil {
			t.Fatalf("expected Dockerfile path %q to be rejected", dockerfile)
		}
	}
}
