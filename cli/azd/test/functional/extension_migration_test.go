// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/stretchr/testify/require"
)

// Test_CLI_Extension_MigrationHostStaging exercises an actual migration-unaware
// host and the current host on the same isolated installed state. The artifacts
// are local fixtures, not evidence of historical public release availability.
func Test_CLI_Extension_MigrationHostStaging(t *testing.T) {
	legacyPath := os.Getenv("CLI_TEST_LEGACY_AZD_PATH")
	if legacyPath == "" {
		t.Skip("Set CLI_TEST_LEGACY_AZD_PATH to the official azd 1.34.2 executable")
	}

	ctx, cancel := newTestContext(t)
	defer cancel()
	dir := tempDirWithDiagnostics(t)
	configDir := filepath.Join(dir, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o700))

	current := azdcli.NewCLI(t)
	current.Env = append(os.Environ(), current.Env...)
	current.Env = append(current.Env,
		"AZD_CONFIG_DIR="+configDir, "AZURE_DEV_COLLECT_TELEMETRY=no",
		"AZD_SKIP_UPDATE_CHECK=true", "NO_COLOR=1", "AZD_FORCE_TTY=false",
	)
	current.WorkingDirectory = dir
	legacy := *current
	legacy.AzdPath = legacyPath
	version, err := legacy.RunCommand(ctx, "version")
	require.NoError(t, err)
	require.Contains(t, version.Stdout, "azd version 1.34.2 ")

	const (
		id        = "test.migration"
		from      = "1.0.47-beta"
		to        = "1.0.0-beta.1"
		source    = "migration-test"
		legacyRun = "legacy artifact"
		newRun    = "corrected artifact"
	)
	entryFile, err := os.CreateTemp(dir, "migration-artifact-*")
	require.NoError(t, err)
	require.NoError(t, entryFile.Close())
	entry := filepath.Base(entryFile.Name())
	registryPath := filepath.Join(dir, "registry.json")
	makeRelease := func(version, content string) extensions.ExtensionVersion {
		t.Helper()
		releaseDir := filepath.Join(dir, version)
		require.NoError(t, os.MkdirAll(releaseDir, 0o700))
		path := filepath.Join(releaseDir, entry)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		sum := sha256.Sum256([]byte(content))
		return extensions.ExtensionVersion{
			Version: version, EntryPoint: entry,
			Artifacts: map[string]extensions.ExtensionArtifact{
				runtime.GOOS + "/" + runtime.GOARCH: {
					URL: path,
					Checksum: extensions.ExtensionChecksum{
						Algorithm: "sha256", Value: hex.EncodeToString(sum[:]),
					},
				},
			},
		}
	}
	oldRelease := makeRelease(from, legacyRun)
	newRelease := makeRelease(to, newRun)
	writeRegistry := func(migrations []extensions.ExtensionVersionMigration, releases ...extensions.ExtensionVersion) {
		t.Helper()
		data, err := json.Marshal(extensions.Registry{
			SchemaVersion: "1.1",
			Extensions: []*extensions.ExtensionMetadata{{
				Id: id, DisplayName: id, VersionMigrations: migrations, Versions: releases,
			}},
		})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(registryPath, data, 0o600))
	}
	run := func(cli *azdcli.CLI, args ...string) *azdcli.CliResult {
		t.Helper()
		result, err := cli.RunCommand(ctx, append(args, "--no-prompt")...)
		require.NoError(t, err)
		return result
	}
	installedVersion := func(cli *azdcli.CLI) string {
		t.Helper()
		result := run(cli, "extension", "list", "--installed", "--source", source, "--output", "json")
		var installed []struct {
			ID               string `json:"id"`
			InstalledVersion string `json:"installedVersion"`
		}
		require.NoError(t, json.Unmarshal([]byte(result.Stdout), &installed))
		require.Len(t, installed, 1)
		require.Equal(t, id, installed[0].ID)
		return installed[0].InstalledVersion
	}
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		return data
	}

	// Seed with an actual old-host install, then expose only the corrected
	// release, matching the production catalogue's absence of the old pin.
	writeRegistry(nil, oldRelease)
	run(&legacy, "extension", "source", "add", "--name", source, "--type", "file", "--location", registryPath)
	run(&legacy, "extension", "install", id, "--source", source, "--version", from)
	require.Equal(t, from, installedVersion(&legacy))
	artifactPath := filepath.Join(configDir, "extensions", id, entry)
	require.Equal(t, legacyRun, string(read(artifactPath)))
	historyPath := filepath.Join(dir, "evaluation-history.json")
	history := []byte(`{"runs":["preserved"]}`)
	require.NoError(t, os.WriteFile(historyPath, history, 0o600))
	configPath := filepath.Join(configDir, "config.json")
	before := read(configPath)

	migrations := []extensions.ExtensionVersionMigration{{From: from, To: to}}
	writeRegistry(migrations, newRelease)
	for _, args := range [][]string{
		{"extension", "update", id, "--source", source},
		{"extension", "update", id, "--source", source, "--version", to},
	} {
		run(&legacy, args...)
		require.Equal(t, from, installedVersion(&legacy))
		require.Equal(t, before, read(configPath))
		require.Equal(t, legacyRun, string(read(artifactPath)))
	}

	// Staging the host must not replace installed extension state. An
	// unavailable successor also must not require resetting that state.
	run(current, "version")
	require.Equal(t, before, read(configPath))
	platform := runtime.GOOS + "/" + runtime.GOARCH
	unavailable := newRelease
	unavailable.Artifacts = map[string]extensions.ExtensionArtifact{
		platform: {URL: filepath.Join(dir, "missing.bin")},
	}
	writeRegistry(migrations, unavailable)
	_, err = current.RunCommand(ctx, "extension", "update", id, "--source", source, "--no-prompt")
	require.Error(t, err)
	require.Equal(t, before, read(configPath))
	require.Equal(t, legacyRun, string(read(artifactPath)))
	require.Equal(t, history, read(historyPath))

	writeRegistry(migrations, newRelease)
	run(current, "extension", "update", id, "--source", source)
	require.Equal(t, to, installedVersion(current))
	require.Equal(t, newRun, string(read(artifactPath)))
	require.Equal(t, history, read(historyPath))
	run(current, "extension", "install", id, "--source", source, "--version", to)
	require.Equal(t, to, installedVersion(current))

	// Migration metadata does not invent a missing historical artifact.
	_, err = current.RunCommand(ctx,
		"extension", "install", id, "--source", source, "--version", from, "--no-prompt")
	require.Error(t, err)
	require.Equal(t, to, installedVersion(current))
	require.Equal(t, newRun, string(read(artifactPath)))
	require.Equal(t, history, read(historyPath))
}
