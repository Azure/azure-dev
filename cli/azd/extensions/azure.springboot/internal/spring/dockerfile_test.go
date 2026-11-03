// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func createMavenProject(t *testing.T) (*azdext.ProjectConfig, *azdext.ServiceConfig) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".mvn", "wrapper"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	for _, path := range []string{"pom.xml", "mvnw", filepath.Join(".mvn", "wrapper", "maven-wrapper.properties")} {
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte("fixture"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "pom.xml"), []byte(`<project><dependencies><dependency>
<groupId>com.h2database</groupId><artifactId>h2</artifactId><scope>runtime</scope>
</dependency></dependencies></project>`), 0o600))
	service := &azdext.ServiceConfig{Name: "petclinic", Host: SpringHost, RelativePath: "."}
	project := &azdext.ProjectConfig{
		Path:     root,
		Services: map[string]*azdext.ServiceConfig{"petclinic": service},
	}
	return project, service
}

func TestServiceConfigurationRejectsUnsupportedTopLevelProperties(t *testing.T) {
	err := validateRawServiceConfiguration("petclinic", map[string]any{
		"host":     SpringHost,
		"language": "java",
		"config":   nil,
		"database": map[string]any{},
		"docker":   map[string]any{},
		"unknown":  true,
	})
	require.ErrorContains(t, err, "config, database, docker, unknown")
	require.NotContains(t, err.Error(), "true")

	config, configErr := structpb.NewStruct(map[string]any{"secret": "do-not-print"})
	require.NoError(t, configErr)
	service := &azdext.ServiceConfig{
		Name:                 "petclinic",
		Config:               config,
		Docker:               &azdext.DockerProjectOptions{},
		AdditionalProperties: config,
	}
	err = validateServiceConfiguration(service)
	require.ErrorContains(t, err, "config, docker, secret")
	require.NotContains(t, err.Error(), "do-not-print")
}

func TestServicePathRejectsTraversal(t *testing.T) {
	project, service := createMavenProject(t)
	service.RelativePath = ".."
	_, err := servicePath(project, service)
	require.ErrorContains(t, err, "outside the azd project")
}

func TestServicePathRejectsLinkedDirectoryOutsideProject(t *testing.T) {
	project, service := createMavenProject(t)
	outside, _ := createMavenProject(t)
	link := filepath.Join(project.Path, "linked")
	if err := os.Symlink(outside.Path, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	service.RelativePath = "linked"

	_, err := servicePath(project, service)
	require.ErrorContains(t, err, "outside the azd project")
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
	require.NoDirExists(t, staged.directory)
	require.NoFileExists(t, filepath.Join(project.Path, "Dockerfile"))
}

func TestPrepareContainerContextIgnoresAuthoredRecipeAndDockerignore(t *testing.T) {
	project, service := createMavenProject(t)
	recipe := []byte("FROM authored\nRUN exit 1\n")
	require.NoError(t, os.WriteFile(filepath.Join(project.Path, "Dockerfile"), recipe, 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(project.Path, ".dockerignore"),
		[]byte("*\n"),
		0o600,
	))

	require.NoError(t, prepareSpringBuild(project, service))
	staged, err := prepareContainerContext(t.Context(), project, service, mavenPackageContext(t, []byte("jar")))
	require.NoError(t, err)
	content, err := os.ReadFile(filepath.Join(project.Path, "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, recipe, content)
	entries, err := os.ReadDir(staged.directory)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	require.NoError(t, staged.cleanup())
	require.NoDirExists(t, filepath.Join(project.Path, stagingDirectoryName))
}

func TestPrepareContainerContextPreservesExistingUserStagingDirectory(t *testing.T) {
	project, service := createMavenProject(t)
	stagingDir := filepath.Join(project.Path, stagingDirectoryName)
	require.NoError(t, os.Mkdir(stagingDir, 0o700))
	sentinel := filepath.Join(stagingDir, "keep.txt")
	require.NoError(t, os.WriteFile(sentinel, []byte("user"), 0o600))

	staged, err := prepareContainerContext(t.Context(), project, service, mavenPackageContext(t, []byte("jar")))
	require.NoError(t, err)
	require.NotEqual(t, stagingDir, staged.directory)
	require.NoError(t, staged.cleanup())
	require.FileExists(t, sentinel)
}

func TestPrepareContainerContextIsolatesConcurrentBuilds(t *testing.T) {
	project, service := createMavenProject(t)
	artifact := mavenPackageContext(t, []byte("jar"))
	first, err := prepareContainerContext(t.Context(), project, service, artifact)
	require.NoError(t, err)
	second, err := prepareContainerContext(t.Context(), project, service, artifact)
	require.NoError(t, err)
	require.NotEqual(t, first.directory, second.directory)
	require.NoError(t, first.cleanup())
	require.FileExists(t, second.jarPath)
	require.NoError(t, second.cleanup())
}

func TestContainerContextCleanupPreservesChangedFiles(t *testing.T) {
	for _, file := range []string{"Dockerfile", stagedJarName} {
		t.Run(file, func(t *testing.T) {
			project, service := createMavenProject(t)
			staged, err := prepareContainerContext(t.Context(), project, service, mavenPackageContext(t, []byte("jar")))
			require.NoError(t, err)
			path := filepath.Join(staged.directory, file)
			require.NoError(t, os.WriteFile(path, []byte("changed"), 0o600))
			require.ErrorContains(t, staged.cleanup(), "changed")
			require.FileExists(t, staged.jarPath)
			require.FileExists(t, staged.recipePath)
			require.NoError(t, os.Remove(staged.jarPath))
			require.NoError(t, os.Remove(staged.recipePath))
			require.NoError(t, os.Remove(staged.directory))
		})
	}
}

func TestPrepareContainerContextHonorsCancellation(t *testing.T) {
	project, service := createMavenProject(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := prepareContainerContext(ctx, project, service, mavenPackageContext(t, []byte("jar")))
	require.ErrorIs(t, err, context.Canceled)
	require.NoDirExists(t, filepath.Join(project.Path, stagingDirectoryName))
}

func TestContainerContextCleanupPreservesReplacedDirectory(t *testing.T) {
	project, service := createMavenProject(t)
	staged, err := prepareContainerContext(t.Context(), project, service, mavenPackageContext(t, []byte("jar")))
	require.NoError(t, err)
	moved := filepath.Join(t.TempDir(), "moved-context")
	require.NoError(t, os.Rename(staged.directory, moved))
	require.NoError(t, os.Mkdir(staged.directory, 0o700))
	sentinel := filepath.Join(staged.directory, "keep.txt")
	require.NoError(t, os.WriteFile(sentinel, []byte("replacement"), 0o600))
	t.Cleanup(func() {
		if _, err := os.Stat(moved); err == nil {
			require.NoError(t, os.Remove(sentinel))
			require.NoError(t, os.Remove(staged.directory))
			require.NoError(t, os.Rename(moved, staged.directory))
		}
		require.NoError(t, staged.cleanup())
	})
	require.ErrorContains(t, staged.cleanup(), "directory changed")
	require.FileExists(t, sentinel)
	require.FileExists(t, filepath.Join(moved, stagedJarName))
	require.NoError(t, os.Remove(sentinel))
	require.NoError(t, os.Remove(staged.directory))
	require.NoError(t, os.Rename(moved, staged.directory))
	require.NoError(t, staged.cleanup())
}

func TestCopyJarDoesNotClaimOrOverwriteExistingFile(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "destination")
	require.NoError(t, os.WriteFile(source, []byte("source"), 0o600))
	require.NoError(t, os.WriteFile(destination, []byte("preserve"), 0o600))
	_, created, err := copyFileWithContext(t.Context(), source, destination)
	require.Error(t, err)
	require.False(t, created)
	content, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(content))
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
	// #nosec G703 -- fixed staged JAR name under a fresh test-owned temporary directory.
	require.NoError(t, os.WriteFile(filepath.Join(packageDir, stagedJarName), content, 0o600))
	return &azdext.ServiceContext{Package: []*azdext.Artifact{{
		Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
		Location:     packageDir,
		LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		Metadata:     map[string]string{"framework": "maven"},
	}}}
}
