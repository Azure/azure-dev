// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func createMavenProject(t *testing.T) (*azdext.ProjectConfig, *azdext.ServiceConfig) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn", "wrapper"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	for _, path := range []string{"pom.xml", "mvnw", filepath.Join(".mvn", "wrapper", "maven-wrapper.properties")} {
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte("fixture"), 0o600))
	}
	service := &azdext.ServiceConfig{Name: "petclinic", Host: SpringHost, RelativePath: "."}
	project := &azdext.ProjectConfig{
		Path:     root,
		Services: map[string]*azdext.ServiceConfig{"petclinic": service},
	}
	return project, service
}

func TestEnsureDockerfileCreatesAndReusesOwnedRecipe(t *testing.T) {
	project, service := createMavenProject(t)
	require.NoError(t, ensureDockerfile(project, service))
	require.NoError(t, ensureDockerfile(project, service))

	data, err := os.ReadFile(filepath.Join(project.Path, "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, springDockerfile, string(data))
	require.NotContains(t, string(data), "SPRING_DATABASE_PASSWORD")
	require.Contains(t, string(data), "USER spring:spring")
}

func TestEnsureDockerfileRejectsConflictingRecipe(t *testing.T) {
	project, service := createMavenProject(t)
	path := filepath.Join(project.Path, "Dockerfile")
	require.NoError(t, os.WriteFile(path, []byte("FROM user-owned"), 0o600))

	err := ensureDockerfile(project, service)
	require.ErrorContains(t, err, "not owned by azure.springboot")
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, "FROM user-owned", string(data))
}

func TestDockerfilePathRejectsTraversal(t *testing.T) {
	project, service := createMavenProject(t)
	service.RelativePath = ".."
	_, err := dockerfilePath(project, service)
	require.ErrorContains(t, err, "outside the azd project")
}

func TestDockerfilePathRejectsLinkedDirectoryOutsideProject(t *testing.T) {
	project, service := createMavenProject(t)
	outside, _ := createMavenProject(t)
	link := filepath.Join(project.Path, "linked")
	if err := os.Symlink(outside.Path, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	service.RelativePath = "linked"

	_, err := dockerfilePath(project, service)
	require.ErrorContains(t, err, "outside the azd project")
}

func TestEnsureDockerfileConcurrentCreation(t *testing.T) {
	project, service := createMavenProject(t)
	var wait sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wait.Go(func() {
			errs <- ensureDockerfile(project, service)
		})
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}
