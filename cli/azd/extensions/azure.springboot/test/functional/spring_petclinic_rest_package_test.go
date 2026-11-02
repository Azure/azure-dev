// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_CLI_SpringPetclinicRest_NativePackage(t *testing.T) {
	if os.Getenv("AZD_TEST_SPRING_PETCLINIC_PACKAGE") != "1" {
		t.Skip("set AZD_TEST_SPRING_PETCLINIC_PACKAGE=1 for the local native REST packaging gate")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	cli := newSpringCLI(t)
	configDir := tempDirWithDiagnostics(t)
	installSpringExtensionFixture(t, ctx, configDir)
	projectDir := tempDirWithDiagnostics(t)
	// #nosec G204 -- fixed repository-owned materializer with a fresh test destination, not a shell command.
	materialize := exec.CommandContext(ctx, "pwsh", "-NoProfile", "-File",
		filepath.Join(petclinicRestFixturePath(t), "materialize.ps1"), "-Destination", projectDir)
	output, err := materialize.CombinedOutput()
	require.NoError(t, err, string(output))
	dockerfile, err := os.ReadFile(filepath.Join(projectDir, "Dockerfile"))
	require.NoError(t, err)
	ignore, err := os.ReadFile(filepath.Join(projectDir, ".dockerignore"))
	require.NoError(t, err)
	var manifest restSourceManifest
	content, err := os.ReadFile(filepath.Join(petclinicRestFixturePath(t), "source-revision.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(content, &manifest))
	envName := "rest-native-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	referencePrefix := "petclinic-rest/spring-petclinic-rest-app-" + envName + ":"
	images := func(ctx context.Context) []string {
		t.Helper()
		return nativeImageTags(t, ctx, referencePrefix)
	}
	require.Empty(t, images(ctx))
	registerNativeImageCleanup(t, referencePrefix)
	cli.WorkingDirectory = projectDir
	cli.Env = environmentWithout(cli.Env, "AZD_CONFIG_DIR")
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+configDir)
	result, err := cli.RunCommand(ctx, "init", "-e", envName, "--no-prompt")
	require.NoError(t, err, result.Stdout+result.Stderr)
	result, err = cli.RunCommand(ctx, "package", "--no-prompt")
	require.NoError(t, err, result.Stdout+result.Stderr)
	require.NotContains(t, strings.ToLower(result.Stdout+result.Stderr), "buildpack")
	packaged := images(ctx)
	require.Len(t, packaged, 1)
	// #nosec G204 -- the image tag is verified by images(), not an arbitrary command or image.
	inspect := exec.CommandContext(ctx, "docker", "image", "inspect", packaged[0], "--format", "{{json .}}")
	output, err = inspect.CombinedOutput()
	require.NoError(t, err, string(output))
	var image struct {
		Architecture string
		OS           string
		Config       struct {
			User       string
			Entrypoint []string
		}
	}
	require.NoError(t, json.Unmarshal(output, &image))
	require.Equal(t, "amd64", image.Architecture)
	require.Equal(t, "linux", image.OS)
	require.Equal(t, "spring:spring", image.Config.User)
	require.Contains(t, image.Config.Entrypoint, "/app/application.jar")
	for path, expected := range manifest.Files {
		actual, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(path)))
		require.NoError(t, err)
		if path == "azure.yaml" {
			// Core init can rewrite YAML line endings; every non-YAML source byte must remain unchanged.
			require.Equal(t, readFixtureFile(t, petclinicRestFixturePath(t), "azure.yaml"),
				strings.ReplaceAll(string(actual), "\r\n", "\n"))
		} else {
			require.Equal(t, expected, gitBlobHash(actual), path)
		}
	}
	after, err := os.ReadFile(filepath.Join(projectDir, "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, dockerfile, after)
	after, err = os.ReadFile(filepath.Join(projectDir, ".dockerignore"))
	require.NoError(t, err)
	require.Equal(t, ignore, after)
	require.NoDirExists(t, filepath.Join(projectDir, ".azd-springboot"))
	assertNativeRestLocalPostgres(t, ctx, packaged[0], projectDir)
}

func nativeImageTags(t *testing.T, ctx context.Context, referencePrefix string) []string {
	t.Helper()
	// #nosec G204 -- callers supply an exact nonce-scoped image prefix as an argument, never shell syntax.
	command := exec.CommandContext(ctx, "docker", "image", "ls", "--filter",
		"reference="+referencePrefix+"*", "--format", "{{.Repository}}:{{.Tag}}")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	tags := strings.Fields(string(output))
	for _, tag := range tags {
		require.True(t, strings.HasPrefix(tag, referencePrefix), "refuse an image outside this test's exact scope")
	}
	return tags
}

func registerNativeImageCleanup(t *testing.T, referencePrefix string) {
	t.Helper()
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, image := range nativeImageTags(t, cleanupCtx, referencePrefix) {
			// #nosec G204 -- nativeImageTags verifies every tag against this test's exact nonce-scoped prefix.
			output, err := exec.CommandContext(cleanupCtx, "docker", "image", "rm", "--no-prune", image).CombinedOutput()
			if err != nil {
				t.Errorf("removing this trial's exact image tag: %v: %s", err, output)
			}
		}
	})
}
