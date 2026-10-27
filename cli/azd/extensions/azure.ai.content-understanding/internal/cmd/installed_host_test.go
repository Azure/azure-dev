// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/require"
)

func TestInstalledHostOffline(t *testing.T) {
	host := os.Getenv("CU_SMOKE_HOST")
	if host == "" {
		t.Skip("Run through ci-smoke.ps1 with a released azd host and built extension")
	}
	artifact := os.Getenv("CU_SMOKE_EXTENSION")
	require.NotEmpty(t, artifact)
	binary, err := filepath.Abs(artifact)
	require.NoError(t, err)
	version := os.Getenv("CU_SMOKE_VERSION")
	require.NotEmpty(t, version)
	body, err := os.ReadFile(binary) // #nosec G304 G703 -- Explicit CI build artifact, not command input.
	require.NoError(t, err)
	digest := sha256.Sum256(body)
	t.Logf("host=%s platform=%s/%s extension=%s sha256=%x", host, runtime.GOOS, runtime.GOARCH, version, digest)

	work := filepath.Join(testutil.TempDir(t), "host smoke 空白")
	require.NoError(t, os.Mkdir(work, 0o700))
	// azd 1.37.0 cannot quote both an installed executable path and a schema argument containing spaces on Windows.
	configDirectory := filepath.Join(testutil.TempDir(t), "config")
	coverage := filepath.Join(work, "coverage")
	require.NoError(t, os.Mkdir(coverage, 0o700))
	environment := []string{}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "AZURE_") || strings.HasPrefix(key, "AZD_") ||
			strings.HasPrefix(key, "CU_") || strings.HasPrefix(key, "ARM_") ||
			key == "HOME" || key == "USERPROFILE" || key == "HTTP_PROXY" ||
			key == "HTTPS_PROXY" || key == "ALL_PROXY" || key == "NO_PROXY" || key == "NO_COLOR" {
			continue
		}
		environment = append(environment, value)
	}
	environment = append(environment,
		"HOME="+work, "USERPROFILE="+work, "AZD_CONFIG_DIR="+configDirectory,
		"AZURE_CONFIG_DIR="+filepath.Join(work, "azure"), "AZURE_DEV_COLLECT_TELEMETRY=no",
		"AZD_SKIP_UPDATE_CHECK=true", "NO_COLOR=1",
		"GOCOVERDIR="+coverage,
		"HTTP_PROXY=http://127.0.0.1:1", "HTTPS_PROXY=http://127.0.0.1:1",
		"ALL_PROXY=http://127.0.0.1:1", "NO_PROXY=localhost,127.0.0.1,::1")
	run := func(success bool, args ...string) []byte {
		t.Helper()
		// Cleanup runs after the test context is canceled; every host call still has a bounded lifetime.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Minute)
		defer cancel()
		// #nosec G204 G702 -- CI supplies the checksum-verified host executable.
		command := exec.CommandContext(ctx, host, append(args, "--no-prompt")...)
		command.Dir = work
		command.Env = environment
		output, err := command.CombinedOutput()
		t.Logf("azd %s", strings.Join(args, " "))
		require.NoError(t, ctx.Err(), string(output))
		if success {
			require.NoError(t, err, string(output))
		} else {
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, string(output))
			require.NotZero(t, exit.ExitCode())
		}
		return output
	}
	require.Contains(t, string(run(true, "version")), "1.37.0")
	registry := map[string]any{
		"schemaVersion": "1.0",
		"extensions": []any{map[string]any{
			"id": extensionID, "namespace": "ai.cu", "displayName": "CU smoke",
			"description": "Offline installed-host smoke",
			"versions": []any{map[string]any{
				"version": version, "requiredAzdVersion": ">=1.37.0",
				"capabilities": []string{"custom-commands", "metadata"},
				"usage":        "azd ai cu <command>",
				"examples": []any{map[string]string{
					"name": "help", "description": "Show help", "usage": "azd ai cu --help",
				}},
				"artifacts": map[string]any{
					runtime.GOOS + "/" + runtime.GOARCH: map[string]any{
						"url": binary, "entryPoint": filepath.Base(binary),
						"checksum": map[string]string{"algorithm": "sha256", "value": hex.EncodeToString(digest[:])},
					},
				},
			}},
		}},
	}
	data, err := json.Marshal(registry)
	require.NoError(t, err)
	path := filepath.Join(work, "registry.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	run(true, "extension", "source", "add", "--name", "cu-smoke", "--type", "file", "--location", path)
	t.Cleanup(func() {
		run(true, "extension", "source", "remove", "cu-smoke")
	})
	run(true, "extension", "install", extensionID, "--source", "cu-smoke", "--version", version)
	uninstalled := false
	t.Cleanup(func() {
		if !uninstalled {
			run(true, "extension", "uninstall", extensionID)
		}
	})
	require.Contains(t, string(run(true, "extension", "list", "--installed", "-o", "json")), extensionID)
	require.Contains(t, string(run(true, "ai", "cu", "--help")), "analyzer")
	var actual map[string]string
	require.NoError(t, json.Unmarshal(run(true, "ai", "cu", "version", "-o", "json"), &actual))
	require.Equal(t, map[string]string{"name": extensionID, "version": version}, actual)

	run(true, "ai", "cu", "config", "set", "CU_ENDPOINT", "https://resource.services.ai.azure.com")
	var userDefaults map[string]string
	require.NoError(t, json.Unmarshal(run(true, "config", "get",
		"extensions.ai-content-understanding"), &userDefaults))
	require.Equal(t, map[string]string{"CU_ENDPOINT": "https://resource.services.ai.azure.com"}, userDefaults)
	run(true, "config", "set", "extensions.ai-content-understanding.CU_API_VERSION", "2027-01-01-preview")
	require.NoError(t, json.Unmarshal(run(true, "ai", "cu", "config", "list", "-o", "json"), &userDefaults))
	require.Equal(t, map[string]string{
		"CU_ENDPOINT": "https://resource.services.ai.azure.com", "CU_API_VERSION": "2027-01-01-preview",
	}, userDefaults)
	run(true, "ai", "cu", "config", "unset", "CU_ENDPOINT")
	run(false, "ai", "cu", "config", "get", "CU_ENDPOINT")

	schema := filepath.Join(work, "schema with spaces.json")
	definition := `{"baseAnalyzerId":"prebuilt-document","models":{"completion":"gpt-4.1"},` +
		`"fieldSchema":{"fields":{"Total":{"type":"number","method":"extract","description":"Synthetic total."}}}}`
	require.NoError(t, os.WriteFile(schema, []byte(definition), 0o600))
	var result struct {
		OK     bool `json:"ok"`
		Strict bool `json:"strict"`
	}
	output := run(true, "ai", "cu", "analyzer", "validate", "--schema", schema, "--strict", "--spec", "-o", "json")
	require.NoError(t, json.Unmarshal(output, &result))
	require.True(t, result.OK)
	require.True(t, result.Strict)
	require.NoError(t, os.WriteFile(schema, []byte("{invalid"), 0o600))
	require.Contains(t, string(run(false, "ai", "cu", "analyzer", "validate", "--schema", schema)), "ERROR")
	run(true, "extension", "uninstall", extensionID)
	uninstalled = true
	// azd 1.37.0 emits consoleMessage events, not [], when nothing is installed.
	require.Contains(t, string(run(true, "extension", "list", "--installed", "-o", "json")), "No extensions installed")
}
