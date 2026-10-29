// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
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
	require.Contains(t, string(data), ".azd-springboot/app.jar")
	require.NotContains(t, string(data), "./mvnw")
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

func TestEnsureDockerfileRejectsSymlink(t *testing.T) {
	project, service := createMavenProject(t)
	target := filepath.Join(t.TempDir(), "Dockerfile")
	require.NoError(t, os.WriteFile(target, []byte(springDockerfile), 0o600))
	if err := os.Symlink(target, filepath.Join(project.Path, "Dockerfile")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	err := ensureDockerfile(project, service)

	require.ErrorContains(t, err, "regular file")
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

func TestEnsureDockerfileMigratesExactLegacyRecipe(t *testing.T) {
	project, service := createMavenProject(t)
	path := filepath.Join(project.Path, "Dockerfile")
	require.NoError(t, os.WriteFile(path, []byte(legacySpringDockerfile), 0o600))

	require.NoError(t, ensureDockerfile(project, service))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, springDockerfile, string(data))
}

func TestPrepareContainerContextStagesMavenJarAndCleansOwnedFiles(t *testing.T) {
	project, service := createMavenProject(t)
	serviceContext := mavenPackageContext(t, []byte("jar-content"))

	staged, err := prepareContainerContext(t.Context(), project, service, serviceContext)
	require.NoError(t, err)
	require.FileExists(t, staged.jarPath)
	content, err := os.ReadFile(staged.jarPath)
	require.NoError(t, err)
	require.Equal(t, []byte("jar-content"), content)
	require.NoError(t, staged.cleanup())
	require.NoDirExists(t, filepath.Join(project.Path, stagingDirectoryName))
	require.FileExists(t, filepath.Join(project.Path, "Dockerfile"))
}

func TestPrepareContainerContextRejectsIgnoredStagingPath(t *testing.T) {
	project, service := createMavenProject(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(project.Path, ".dockerignore"),
		[]byte(".azd-springboot\n"),
		0o600,
	))

	_, err := prepareContainerContext(t.Context(), project, service, mavenPackageContext(t, []byte("jar")))
	require.ErrorContains(t, err, "excludes required build path")
	require.NoDirExists(t, filepath.Join(project.Path, stagingDirectoryName))
	require.NoFileExists(t, filepath.Join(project.Path, "Dockerfile"))
}

func TestPrepareContainerContextRejectsCollisionWithoutDeletingUserFiles(t *testing.T) {
	project, service := createMavenProject(t)
	stagingDir := filepath.Join(project.Path, stagingDirectoryName)
	require.NoError(t, os.Mkdir(stagingDir, 0o700))
	sentinel := filepath.Join(stagingDir, "keep.txt")
	require.NoError(t, os.WriteFile(sentinel, []byte("user"), 0o600))

	_, err := prepareContainerContext(t.Context(), project, service, mavenPackageContext(t, []byte("jar")))
	require.ErrorContains(t, err, "already contains")
	require.FileExists(t, sentinel)
}

func TestPrepareContainerContextHonorsCancellation(t *testing.T) {
	project, service := createMavenProject(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := prepareContainerContext(ctx, project, service, mavenPackageContext(t, []byte("jar")))
	require.ErrorIs(t, err, context.Canceled)
	require.NoDirExists(t, filepath.Join(project.Path, stagingDirectoryName))
}

func TestFindMavenJarRejectsSymlink(t *testing.T) {
	project, service := createMavenProject(t)
	packageDir := t.TempDir()
	target := filepath.Join(packageDir, "target.jar")
	require.NoError(t, os.WriteFile(target, []byte("jar"), 0o600))
	if err := os.Symlink(target, filepath.Join(packageDir, stagedJarName)); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	serviceContext := &azdext.ServiceContext{Package: []*azdext.Artifact{{
		Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
		Location:     packageDir,
		LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		Metadata:     map[string]string{"framework": "maven"},
	}}}

	_, err := prepareContainerContext(t.Context(), project, service, serviceContext)
	require.ErrorContains(t, err, "regular file")
}

func mavenPackageContext(t *testing.T, content []byte) *azdext.ServiceContext {
	t.Helper()
	packageDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(packageDir, stagedJarName), content, 0o600))
	return &azdext.ServiceContext{Package: []*azdext.Artifact{{
		Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
		Location:     packageDir,
		LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		Metadata:     map[string]string{"framework": "maven"},
	}}}
}
