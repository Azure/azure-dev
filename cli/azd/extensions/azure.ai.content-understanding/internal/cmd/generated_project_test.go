// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/infra"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type generatedProjectFixture struct {
	Choices             infra.Choices     `json:"choices"`
	Hashes              map[string]string `json:"hashes"`
	NativeHashes        map[string]string `json:"native_hashes"`
	HookArguments       []string          `json:"hook_arguments"`
	NativeHookArguments []string          `json:"native_hook_arguments"`
	Config              json.RawMessage   `json:"config"`
}

func materializeContractProject(t *testing.T) (string, generatedProjectFixture) {
	t.Helper()
	var fixture generatedProjectFixture
	loadCommandFixture(t, loadParityInventory(t).GeneratedProjects, &fixture)
	project := filepath.Join(testutil.TempDir(t), "project with spaces")
	result, err := infra.Materialize(t.Context(), project, fixture.Choices, false)
	require.NoError(t, err)
	require.False(t, result.ReusedExistingTemplate)
	paths := make([]string, 0, len(fixture.Hashes))
	for path := range fixture.Hashes {
		paths = append(paths, path)
	}
	require.ElementsMatch(t, paths, result.Files)
	return project, fixture
}

func TestGeneratedProjectTree(t *testing.T) {
	project, fixture := materializeContractProject(t)
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
	for path, hash := range fixture.NativeHashes {
		require.Contains(t, fixture.Hashes, path)
		fixture.Hashes[path] = hash
	}
	require.Equal(t, fixture.Hashes, actual)
	config, err := directory.ReadFile(".azure/config.json")
	require.NoError(t, err)
	assertJSONEqual(t, fixture.Config, json.RawMessage(config))
	if runtime.GOOS != "windows" {
		info, err := directory.Stat("hooks/postprovision.sh")
		require.NoError(t, err)
		require.NotZero(t, info.Mode().Perm()&0o100)
	}
}

func TestGeneratedBashHookArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher; PowerShell launcher is tested separately")
	}
	executable, err := exec.LookPath("bash")
	require.NoError(t, err)
	project, fixture := materializeContractProject(t)
	bin := testutil.TempDir(t)
	log := filepath.Join(testutil.TempDir(t), "arguments.txt")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CU_TEST_LOG\"\n"
	stubPath := filepath.Join(bin, "azd")
	// #nosec G306 -- Private executable test host.
	require.NoError(t, os.WriteFile(stubPath, []byte(stub), 0o700))
	command := exec.CommandContext(t.Context(), executable, "hooks/postprovision.sh") // #nosec G204 -- Test shell.
	command.Dir = project
	command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CU_TEST_LOG="+log, "AZURE_ENV_NAME=prod", "AZD_ENVIRONMENT=dev")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Empty(t, output)
	recorded, err := os.ReadFile(log) // #nosec G304 -- Private fake-host argument log.
	require.NoError(t, err)
	actual := strings.Split(strings.TrimSuffix(string(recorded), "\n"), "\n")
	require.Equal(t, []string{"ai", "cu", "infra", "_postprovision-v1", "--environment", "prod"}, actual)
	require.Equal(t, fixture.NativeHookArguments, actual)
	require.Equal(t, fixture.HookArguments, actual[:4])
}

func TestGeneratedPowerShellHook(t *testing.T) {
	executable, err := exec.LookPath("pwsh")
	if runtime.GOOS != "windows" && err != nil {
		t.Skip("PowerShell is required; this test is mandatory on Windows CI")
	}
	require.NoError(t, err)
	for _, row := range []struct {
		name             string
		exit             int
		missing          bool
		emptyEnvironment bool
	}{
		{name: "success"},
		{name: "failure", exit: 37},
		{name: "missing azd", exit: 1, missing: true},
		{name: "empty environment", exit: 1, emptyEnvironment: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			project, fixture := materializeContractProject(t)
			bin := filepath.Join(testutil.TempDir(t), "fake host with spaces")
			require.NoError(t, os.Mkdir(bin, 0o700))
			log := filepath.Join(testutil.TempDir(t), "arguments.txt")
			if !row.missing {
				name := "azd"
				stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CU_TEST_LOG\"\nexit \"$CU_TEST_EXIT\"\n"
				if runtime.GOOS == "windows" {
					name = "azd.cmd"
					stub = "@echo off\r\n@echo %*> \"%CU_TEST_LOG%\"\r\n@exit /b %CU_TEST_EXIT%\r\n"
				}
				// #nosec G306 -- Private executable test host.
				require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(stub), 0o700))
			}
			command := exec.CommandContext(t.Context(), executable, // #nosec G204 -- Test shell.
				"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
				"-File", "hooks/postprovision.ps1")
			command.Dir = project
			environment := "prod"
			if row.emptyEnvironment {
				environment = ""
			}
			command.Env = append(os.Environ(), "PATH="+bin, "CU_TEST_LOG="+log,
				"AZURE_ENV_NAME="+environment, "AZD_ENVIRONMENT=dev",
				fmt.Sprintf("CU_TEST_EXIT=%d", row.exit))
			output, err := command.CombinedOutput()
			if row.exit == 0 {
				require.NoError(t, err, string(output))
			} else {
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit, string(output))
				assert.Equal(t, row.exit, exit.ExitCode(), string(output))
			}
			if row.missing {
				assert.Contains(t, string(output), "Postprovision requires azd")
				_, err := os.Stat(log)
				require.ErrorIs(t, err, os.ErrNotExist)
				return
			}
			if row.emptyEnvironment {
				assert.Contains(t, string(output), "Postprovision requires AZURE_ENV_NAME")
				assert.NoFileExists(t, log)
				return
			}
			require.Empty(t, output)
			recorded, err := os.ReadFile(log) // #nosec G304 -- Private fake-host argument log.
			require.NoError(t, err)
			require.Equal(t, fixture.NativeHookArguments, strings.Fields(string(recorded)))
		})
	}
}
