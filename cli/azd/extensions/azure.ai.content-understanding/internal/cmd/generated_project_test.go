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

type generatedProjectFixture struct {
	Choices       infra.Choices     `json:"choices"`
	Hashes        map[string]string `json:"hashes"`
	HookArguments []string          `json:"hook_arguments"`
	Config        json.RawMessage   `json:"config"`
}

func materializeContractProject(t *testing.T) (string, generatedProjectFixture) {
	t.Helper()
	var fixture generatedProjectFixture
	loadCommandFixture(t, loadParityInventory(t).GeneratedProjects, &fixture)
	project := filepath.Join(t.TempDir(), "project with spaces")
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
		t.Skip("POSIX azd stub; Windows hook runtime evidence remains pending")
	}
	executable, err := exec.LookPath("bash")
	require.NoError(t, err)
	project, fixture := materializeContractProject(t)
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
	require.Equal(t, []string{"ai", "cu", "infra", "_postprovision-v1"}, actual)
	require.Equal(t, fixture.HookArguments, actual)
}
