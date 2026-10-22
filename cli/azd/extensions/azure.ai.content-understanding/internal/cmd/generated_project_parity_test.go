// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/infra"

	"github.com/stretchr/testify/require"
)

var generatedProjectFrontends = []string{"core", "azure-cli"}

type generatedProjectOracle struct {
	SchemaVersion int `json:"schema_version"`
	Source        struct {
		Repository string            `json:"repository"`
		Commit     string            `json:"commit"`
		License    string            `json:"license"`
		Files      map[string]string `json:"files"`
		Python     string            `json:"python"`
	} `json:"source"`
	Canonical struct {
		SchemaVersion int `json:"schemaVersion"`
		Choices       struct {
			Environment             string  `json:"environment"`
			Location                string  `json:"location"`
			APIVersion              string  `json:"apiVersion"`
			SubscriptionID          string  `json:"subscriptionId"`
			SubscriptionName        string  `json:"subscriptionName"`
			TenantID                string  `json:"tenantId"`
			FoundryPrefix           string  `json:"foundryPrefix"`
			FoundryEndpoint         *string `json:"foundryEndpoint"`
			FoundryResourceGroup    *string `json:"foundryResourceGroup"`
			ModelSelection          string  `json:"modelSelection"`
			AssignRoles             bool    `json:"assignRoles"`
			ForceConfigurationSetup bool    `json:"forceConfigurationSetup"`
		} `json:"choices"`
		Hashes        map[string]string `json:"normalizedSha256"`
		HookArguments []string          `json:"hiddenHookArguments"`
	} `json:"canonical"`
	SourceConfig json.RawMessage `json:"source_config"`
	Frontends    []struct {
		Name          string   `json:"name"`
		HookArguments []string `json:"hook_arguments"`
	} `json:"frontends"`
	NativeDifferences map[string]struct {
		SHA256    string `json:"sha256"`
		Rationale string `json:"rationale"`
	} `json:"native_differences"`
	Boundaries []string `json:"boundaries"`
}

func loadGeneratedProjectOracle(t *testing.T) generatedProjectOracle {
	t.Helper()
	inventory := loadParityInventory(t)
	require.Equal(t, "testdata/parity/generated_projects.json", inventory.GeneratedProjects.File)
	body, err := os.ReadFile("../../testdata/parity/generated_projects.json")
	require.NoError(t, err)
	require.Equal(t, inventory.GeneratedProjects.SHA256, sha256Text(body))
	require.NoError(t, recordingHygiene(body))
	_, err = recordedJSON(body)
	require.NoError(t, err)
	var oracle generatedProjectOracle
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&oracle))
	require.Equal(t, 1, oracle.SchemaVersion)
	require.Equal(t, inventory.Source.Repository, oracle.Source.Repository)
	require.Equal(t, paritySourceCommit, oracle.Source.Commit)
	require.Equal(t, "MIT", oracle.Source.License)
	require.NotEmpty(t, oracle.Source.Python)
	require.Len(t, oracle.Source.Files, 4)
	for path, digest := range oracle.Source.Files {
		require.NotContains(t, path, "..")
		require.Regexp(t, "^(tests|packages)/", path)
		require.Regexp(t, "^[0-9a-f]{64}$", digest)
	}
	require.Equal(t, 1, oracle.Canonical.SchemaVersion)
	require.Len(t, oracle.Canonical.Hashes, 10)
	require.Equal(t, []string{"ai", "cu", "infra", "_postprovision-v1"}, oracle.Canonical.HookArguments)
	require.Len(t, oracle.Frontends, len(generatedProjectFrontends))
	for index, frontend := range oracle.Frontends {
		require.Equal(t, generatedProjectFrontends[index], frontend.Name)
		require.Equal(t, oracle.Canonical.HookArguments, frontend.HookArguments)
	}
	require.Len(t, oracle.NativeDifferences, 2)
	for _, path := range []string{"README.md", ".azure/config.json"} {
		difference, exists := oracle.NativeDifferences[path]
		require.True(t, exists)
		require.Regexp(t, "^[0-9a-f]{64}$", difference.SHA256)
		require.NotEqual(t, oracle.Canonical.Hashes[path], difference.SHA256)
		require.NotEmpty(t, difference.Rationale)
	}
	require.Len(t, oracle.Boundaries, 3)
	return oracle
}

func materializeParityProject(t *testing.T, oracle generatedProjectOracle) (string, infra.Generation) {
	t.Helper()
	source := oracle.Canonical.Choices
	choices := infra.Choices{
		Environment: source.Environment, Location: source.Location, APIVersion: source.APIVersion,
		SubscriptionID: source.SubscriptionID, SubscriptionName: source.SubscriptionName, TenantID: source.TenantID,
		FoundryPrefix: source.FoundryPrefix, ModelSelection: source.ModelSelection,
		AssignRoles: source.AssignRoles, ForceConfigurationSetup: source.ForceConfigurationSetup,
	}
	if source.FoundryEndpoint != nil {
		choices.FoundryEndpoint = *source.FoundryEndpoint
	}
	if source.FoundryResourceGroup != nil {
		choices.FoundryResourceGroup = *source.FoundryResourceGroup
	}
	project := filepath.Join(t.TempDir(), "project with spaces")
	result, err := infra.Materialize(t.Context(), project, choices, false)
	require.NoError(t, err)
	require.False(t, result.ReusedExistingTemplate)
	paths := make([]string, 0, len(oracle.Canonical.Hashes))
	for path := range oracle.Canonical.Hashes {
		paths = append(paths, path)
	}
	require.ElementsMatch(t, paths, result.Files)
	return project, result
}

func TestGeneratedProjectTreeParity(t *testing.T) {
	oracle := loadGeneratedProjectOracle(t)
	for _, frontend := range oracle.Frontends {
		t.Run(frontend.Name, func(t *testing.T) {
			project, _ := materializeParityProject(t, oracle)
			directory, err := os.OpenRoot(project)
			require.NoError(t, err)
			defer directory.Close()
			actual := map[string]string{}
			err = fs.WalkDir(directory.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				require.True(t, entry.Type().IsRegular())
				body, err := directory.ReadFile(path)
				if err != nil {
					return err
				}
				actual[path] = sha256Text(bytes.ReplaceAll(body, []byte("\r\n"), []byte("\n")))
				return nil
			})
			require.NoError(t, err)
			expected := map[string]string{}
			for path, digest := range oracle.Canonical.Hashes {
				expected[path] = digest
			}
			for path, difference := range oracle.NativeDifferences {
				expected[path] = difference.SHA256
			}
			require.Equal(t, expected, actual)
			config, err := os.ReadFile(filepath.Join(project, ".azure", "config.json")) // #nosec G304 -- Private test state.
			require.NoError(t, err)
			assertNormalizedJSON(t, oracle.SourceConfig, json.RawMessage(config))
			if runtime.GOOS != "windows" {
				info, err := os.Stat(filepath.Join(project, "hooks", "postprovision.sh"))
				require.NoError(t, err)
				require.NotZero(t, info.Mode().Perm()&0o100)
			}
		})
	}
}

func TestGeneratedBashHookParity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX azd stub; Windows hook runtime evidence remains pending")
	}
	executable, err := exec.LookPath("bash")
	require.NoError(t, err)
	oracle := loadGeneratedProjectOracle(t)
	for _, frontend := range oracle.Frontends {
		t.Run(frontend.Name, func(t *testing.T) {
			project, _ := materializeParityProject(t, oracle)
			bin := t.TempDir()
			log := filepath.Join(t.TempDir(), "arguments.txt")
			stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CU_TEST_LOG\"\n"
			stubPath := filepath.Join(bin, "azd")
			// #nosec G306 -- Private executable test host.
			require.NoError(t, os.WriteFile(stubPath, []byte(stub), 0o700))
			command := exec.CommandContext(t.Context(), executable, "hooks/postprovision.sh") // #nosec G204 -- Test shell.
			command.Dir = project
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"CU_TEST_LOG="+log)
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			require.Empty(t, output)
			recorded, err := os.ReadFile(log) // #nosec G304 -- Private fake-host argument log.
			require.NoError(t, err)
			actual := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
			require.Equal(t, frontend.HookArguments, actual)
			require.Equal(t, oracle.Canonical.HookArguments, actual)
		})
	}
}
