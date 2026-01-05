// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/stretchr/testify/require"
)

const (
	localRegistryAddr = "localhost:5000"
	imageName         = "azd-acceptance-test"
)

// Test_DockerAcceptance validates the Docker CLI tool works end-to-end.
// Opt-in test: only runs if AZD_TEST_DOCKER_E2E=1
// Requires: Container runtime (Docker or Podman) installed and running
// Requires: Local registry at localhost:5000, OR set REGISTRY_SERVER for Azure Container Registry
//
// Example:
//
//	AZD_TEST_DOCKER_E2E=1 AZD_CONTAINER_RUNTIME=podman REGISTRY_SERVER='myregistry.azurecr.io' go test .
func Test_DockerAcceptance(t *testing.T) {
	if os.Getenv("AZD_TEST_DOCKER_E2E") != "1" {
		t.Skip("Skipping Docker acceptance test. Set AZD_TEST_DOCKER_E2E=1 to enable.")
	}

	ctx := context.Background()
	cli := docker.NewCli(exec.NewCommandRunner(nil))

	// 1. CheckInstalled - validates runtime is available
	err := cli.CheckInstalled(ctx)
	require.NoError(t, err, "container runtime should be installed and running")

	t.Logf("Using container runtime: %s", cli.Name())

	// Determine registry - use Azure Container Registry if REGISTRY_SERVER is set
	registryServer := os.Getenv("REGISTRY_SERVER")
	if registryServer != "" {
		t.Logf("Using Azure Container Registry: %s", registryServer)
		loginToAzureRegistry(t, ctx, cli, registryServer)
	} else {
		registryServer = localRegistryAddr
		t.Logf("Using local registry: %s", registryServer)
	}

	// 2. Build - build an image from Dockerfile with build args and target
	cwd, err := os.Getwd()
	require.NoError(t, err)

	dockerfilePath := filepath.Join(cwd, "testdata", "Dockerfile")
	tag := fmt.Sprintf("%s:%d", imageName, time.Now().Unix())

	buildArgs := []string{
		fmt.Sprintf("BUILD_VERSION=%s", "1.0.0-test"),
		fmt.Sprintf("BUILD_DATE=%s", time.Now().Format(time.RFC3339)),
	}

	// Create a temporary secret file for build secrets
	dir := t.TempDir()
	secretFile, err := os.CreateTemp(dir, "azd-test-secret-*")
	require.NoError(t, err)
	defer os.Remove(secretFile.Name())
	_, err = secretFile.WriteString("test-secret-value")
	require.NoError(t, err)
	require.NoError(t, secretFile.Close())

	buildSecrets := []string{
		fmt.Sprintf("id=test_secret,src=%s", secretFile.Name()),
	}

	buildEnv := []string{
		"DOCKER_BUILDKIT=1",
	}

	var buildOutput bytes.Buffer
	imageID, err := cli.Build(
		ctx,
		cwd,
		dockerfilePath,
		"linux/amd64",
		"final",
		filepath.Join(cwd, "testdata"),
		tag,
		buildArgs,
		buildSecrets,
		buildEnv,
		&buildOutput,
	)
	require.NoError(t, err, "build should succeed")
	require.NotEmpty(t, imageID, "build should return image ID")
	t.Logf("Built image: %s (ID: %s)", tag, imageID)

	// Cleanup: remove local images at end
	remoteTag := fmt.Sprintf("%s/%s", registryServer, tag)
	defer func() {
		_ = cli.Remove(ctx, tag)
		_ = cli.Remove(ctx, remoteTag)
	}()

	// 3. Tag - tag image for registry
	err = cli.Tag(ctx, cwd, tag, remoteTag)
	require.NoError(t, err, "tag should succeed")
	t.Logf("Tagged image: %s", remoteTag)

	// 4. Push - push to registry
	err = cli.Push(ctx, cwd, remoteTag)
	require.NoError(t, err, "push should succeed")
	t.Logf("Pushed image: %s", remoteTag)

	// 5. Remove local image to verify pull works
	err = cli.Remove(ctx, remoteTag)
	require.NoError(t, err, "remove should succeed")
	t.Logf("Removed local image: %s", remoteTag)

	// 6. Pull - pull from registry
	err = cli.Pull(ctx, remoteTag)
	require.NoError(t, err, "pull should succeed")
	t.Logf("Pulled image: %s", remoteTag)

	// 7. Inspect - verify pulled image
	output, err := cli.Inspect(ctx, remoteTag, "{{.Id}}")
	require.NoError(t, err, "inspect should succeed")
	require.NotEmpty(t, output, "inspect should return image info")
	t.Logf("Inspected image ID: %s", strings.TrimSpace(output))

	// 8. IsContainerdEnabled - should work without error
	_, err = cli.IsContainerdEnabled(ctx)
	require.NoError(t, err, "IsContainerdEnabled should not error")
}

// loginToAzureRegistry logs into Azure Container Registry using managed identity
func loginToAzureRegistry(t *testing.T, ctx context.Context, cli *docker.Cli, registryServer string) {
	t.Helper()

	// Extract registry name from server (e.g., "myregistry.azurecr.io" -> "myregistry")
	registryName := strings.Split(registryServer, ".")[0]

	// Get access token using managed identity
	cmd := osexec.CommandContext(ctx, "az", "acr", "login",
		"--name", registryName,
		"--expose-token",
		"--query", "accessToken",
		"--output", "tsv",
	)
	tokenOutput, err := cmd.Output()
	require.NoError(t, err, "az acr login --expose-token should succeed")

	token := strings.TrimSpace(string(tokenOutput))
	require.NotEmpty(t, token, "access token should not be empty")

	// Login with managed identity UUID as username and token as password
	err = cli.Login(ctx, registryServer, "00000000-0000-0000-0000-000000000000", token)
	require.NoError(t, err, "docker login to ACR should succeed")
	t.Logf("Logged into Azure Container Registry: %s", registryServer)
}
