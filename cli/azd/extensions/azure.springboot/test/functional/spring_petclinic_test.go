// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func Test_CLI_SpringExtension_BundleInstallsWithoutBaseTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	installSpringExtensionFixture(t, ctx, tempDirWithDiagnostics(t))
}

func environmentWithout(environment []string, keys ...string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if slices.ContainsFunc(keys, func(key string) bool {
			return strings.EqualFold(name, key)
		}) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func installSpringExtensionFixture(t *testing.T, ctx context.Context, configDir string) {
	t.Helper()
	extensionSourceDir := filepath.Join(azdSourcePath(t), "extensions", "azure.springboot")
	extensionDir := tempDirWithDiagnostics(t)
	require.NoError(t, os.CopyFS(extensionDir, extensionSourceFS{os.DirFS(extensionSourceDir)}))
	evidenceDir := retainSpringAcceptanceSource(t, extensionDir)
	bundlePath := filepath.Join(tempDirWithDiagnostics(t), "azure.springboot_0.1.0.zip")

	cli := newSpringCLI(t)
	cli.WorkingDirectory = extensionDir
	cli.Env = environmentWithout(cli.Env, "AZD_CONFIG_DIR")
	cli.Env = append(cli.Env, "AZD_CONFIG_DIR="+configDir)

	installExtensionDeveloperToolFastStub(t, ctx, configDir)

	_, err := cli.RunCommand(ctx, "x", "build", "--skip-install")
	require.NoError(t, err)

	_, err = cli.RunCommand(ctx, "x", "pack", "--bundle", "--output", bundlePath)
	require.NoError(t, err)
	require.FileExists(t, bundlePath)
	if evidenceDir != "" {
		source, err := os.Open(bundlePath)
		require.NoError(t, err)
		// #nosec G703 -- fixed bundle name in the fresh private evidence directory created below.
		target, err := os.OpenFile(filepath.Join(evidenceDir, "bundle.zip"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			require.NoError(t, source.Close())
			require.NoError(t, err)
		}
		_, copyErr := io.Copy(target, source)
		require.NoError(t, errors.Join(copyErr, source.Close(), target.Close()))
	}

	_, err = cli.RunCommand(ctx, "extension", "install", bundlePath)
	require.NoError(t, err)

	var config struct {
		Extension struct {
			Installed map[string]*struct {
				Source    string `json:"source"`
				Providers []struct {
					Name       string  `json:"name"`
					Type       string  `json:"type"`
					BaseTarget *string `json:"baseTarget"`
				} `json:"providers"`
			} `json:"installed"`
		} `json:"extension"`
	}

	configPath := filepath.Join(configDir, "config.json")
	// #nosec G703 -- fixed config.json under the isolated azd test configuration directory.
	content, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(content, &config))
	installed, ok := config.Extension.Installed["azure.springboot"]
	require.True(t, ok, "bundle-installed extension missing from config")
	require.NotNil(t, installed)
	require.Equal(t, "bundle", installed.Source)
	require.Len(t, installed.Providers, 1)
	require.Equal(t, "springbootapp", installed.Providers[0].Name)
	require.Equal(t, "service-target", installed.Providers[0].Type)
	require.Nil(t, installed.Providers[0].BaseTarget)
}

func retainSpringAcceptanceSource(t *testing.T, source string) string {
	t.Helper()
	root := os.Getenv("AZD_TEST_SPRING_ARTIFACTS_DIR")
	if root == "" {
		return ""
	}
	require.True(t, filepath.IsAbs(root), "acceptance evidence must use an explicitly selected absolute directory")
	directory, err := os.MkdirTemp(root, "spring-native-")
	require.NoError(t, err)
	// #nosec G703 -- fixed source subdirectory under a fresh private os.MkdirTemp evidence directory.
	require.NoError(t, os.CopyFS(filepath.Join(directory, "source"), extensionSourceFS{os.DirFS(source)}))
	// #nosec G703 -- fixed receipt filename under that same fresh private evidence directory.
	require.NoError(t, os.WriteFile(filepath.Join(directory, "test-name.txt"), []byte(t.Name()+"\n"), 0o600))
	t.Logf("retained exact acceptance build inputs and bundle: %s", directory)
	return directory
}

func verifyH2ContainerStartup(t *testing.T, ctx context.Context, image string) {
	t.Helper()
	name := "azd-springboot-h2-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	const ownerLabel = "azd.spring.h2.owner"
	dockerEnvironment := append(environmentWithout(os.Environ(), "DOCKER_CONFIG"),
		"DOCKER_CONFIG="+tempDirWithDiagnostics(t))
	// #nosec G204 -- verified packaged image and generated ownership/name arguments, without shell interpolation.
	run := osexec.CommandContext(
		ctx,
		"docker",
		"run",
		"--detach",
		"--name",
		name,
		"--label",
		ownerLabel+"="+name,
		"--publish",
		"127.0.0.1::8080",
		image,
		"--management.endpoint.health.show-details=always",
	)
	run.Env = dockerEnvironment
	output, err := run.CombinedOutput()
	require.NoError(t, err, "starting packaged H2 container: %s", output)
	containerID := strings.TrimSpace(string(output))
	require.Len(t, containerID, 64)
	_, err = hex.DecodeString(containerID)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		// #nosec G204 -- validated 64-character hexadecimal Docker ID; this read verifies its exact ownership label.
		inspect := osexec.CommandContext(cleanupCtx, "docker", "inspect", "--format",
			`{{index .Config.Labels "azd.spring.h2.owner"}}`, containerID)
		inspect.Env = dockerEnvironment
		label, err := inspect.CombinedOutput()
		if err != nil || strings.TrimSpace(string(label)) != name {
			t.Errorf("refusing cleanup of an unverifiable H2 container: %v", err)
			return
		}
		// #nosec G204 -- validated Docker ID with the exact test-owned label verified immediately above.
		remove := osexec.CommandContext(cleanupCtx, "docker", "rm", "--force", containerID)
		remove.Env = dockerEnvironment
		output, err := remove.CombinedOutput()
		if err != nil {
			t.Errorf("removing this test's owned H2 container: %v: %s", err, output)
		}
	})

	// #nosec G204 -- validated Docker container ID and fixed inventory arguments, without shell interpolation.
	portCommand := osexec.CommandContext(ctx, "docker", "port", containerID, "8080/tcp")
	portCommand.Env = dockerEnvironment
	output, err = portCommand.CombinedOutput()
	require.NoError(t, err, "reading H2 container port: %s", output)
	address := strings.TrimSpace(string(output))
	require.NotEmpty(t, address)
	if after, ok := strings.CutPrefix(address, "0.0.0.0:"); ok {
		address = "127.0.0.1:" + after
	}
	endpoint := "http://" + address + "/actuator/health"

	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(4 * time.Minute)
	var lastResponse string
	for time.Now().Before(deadline) {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if requestErr != nil {
			require.NoError(t, requestErr)
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			closeErr := response.Body.Close()
			if readErr == nil {
				lastResponse = string(body)
				if response.StatusCode == http.StatusOK && strings.Contains(lastResponse, `"status":"UP"`) {
					require.NoError(t, closeErr)
					require.Contains(t, lastResponse, `"database":"H2"`)
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			require.NoError(t, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	// #nosec G204 -- validated Docker container ID and read-only diagnostics, without shell interpolation.
	logCommand := osexec.CommandContext(ctx, "docker", "logs", containerID)
	logCommand.Env = dockerEnvironment
	logs, _ := logCommand.CombinedOutput()
	t.Fatalf("packaged H2 container did not become healthy; last response %q; logs:\n%s", lastResponse, logs)
}

func installExtensionDeveloperToolFastStub(t *testing.T, ctx context.Context, configDir string) {
	t.Helper()
	binaryName := "microsoft-azd-extensions"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	relativePath := filepath.Join("extensions", "microsoft.azd.extensions", binaryName)
	absolutePath := filepath.Join(configDir, relativePath)
	// #nosec G703 -- fixed developer-tool path under the isolated test configuration directory.
	require.NoError(t, os.MkdirAll(filepath.Dir(absolutePath), 0o755))

	// #nosec G204 G702 -- fixed package and fresh test-owned output path, without shell interpolation.
	build := osexec.CommandContext(ctx, "go", "build", "-o", absolutePath, ".")
	build.Dir = filepath.Join(azdSourcePath(t), "extensions", "microsoft.azd.extensions")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "building developer extension fast stub: %s", output)

	config := map[string]any{}
	configPath := filepath.Join(configDir, "config.json")
	// #nosec G703 -- fixed config.json under the isolated test configuration directory.
	if content, err := os.ReadFile(configPath); err == nil {
		require.NoError(t, json.Unmarshal(content, &config))
	} else {
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	extensionSection, _ := config["extension"].(map[string]any)
	if extensionSection == nil {
		extensionSection = map[string]any{}
		config["extension"] = extensionSection
	}
	installed, _ := extensionSection["installed"].(map[string]any)
	if installed == nil {
		installed = map[string]any{}
		extensionSection["installed"] = installed
	}
	installed["microsoft.azd.extensions"] = &extensions.Extension{
		Id:          "microsoft.azd.extensions",
		Namespace:   "x",
		Version:     "0.14.0",
		Path:        relativePath,
		DisplayName: "azd extensions Developer Kit",
		Description: "Local functional-test developer tooling.",
		Usage:       "azd x <command> [options]",
		Capabilities: []extensions.CapabilityType{
			extensions.CustomCommandCapability,
			extensions.MetadataCapability,
		},
	}
	content, err := json.Marshal(config)
	require.NoError(t, err)
	// #nosec G703 -- fixed config.json under the isolated test configuration directory.
	require.NoError(t, os.WriteFile(configPath, content, 0o600))
}
