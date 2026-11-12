// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package testutil

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// ACRParameters writes a supported parameter-only Foundry project.
func ACRParameters(t testing.TB, root string, layer bool, format, id string) {
	t.Helper()
	t.Setenv("AZD_CONFIG_DIR", t.TempDir())
	manifest := "infra:\n  provider: microsoft.foundry\n"
	dir, module := filepath.Join(root, "infra"), "main"
	if layer {
		manifest = "infra:\n  layers:\n" +
			"    - name: foundry\n      provider: microsoft.foundry\n" +
			"      path: infra/foundry\n      module: project\n"
		dir, module = filepath.Join(dir, "foundry"), "project"
	}
	manifest += "services:\n  project:\n    host: azure.ai.project\n" +
		"  agent:\n    host: azure.ai.agent\n    kind: hosted\n    name: agent\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(manifest), 0o600))
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, module+".bicep"),
		[]byte("param existingAcrResourceId string = ''\n"), 0o600))
	parameters, err := json.Marshal(map[string]any{
		"parameters": map[string]any{"existingAcrResourceId": map[string]any{"value": id}},
	})
	require.NoError(t, err)
	if format == "json" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, module+".parameters.json"), parameters, 0o600))
		return
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, module+".bicepparam"),
		[]byte("using './"+module+".bicep'\nparam existingAcrResourceId = '"+id+"'\n"), 0o600))
	StubBicepParameters(t, string(parameters))
}

// StubBicepParameters replaces compilation with a local test fixture.
func StubBicepParameters(t testing.TB, parameters string) {
	t.Helper()
	dir := t.TempDir()
	output, err := json.Marshal(map[string]string{
		"templateJson":   `{"parameters":{"existingAcrResourceId":{"defaultValue":""}},"resources":[]}`,
		"parametersJson": parameters,
	})
	require.NoError(t, err)
	result := filepath.Join(dir, "result.json")
	require.NoError(t, os.WriteFile(result, output, 0o600))
	name, script := "bicep", "#!/bin/sh\ncat \"$AZD_TEST_BICEP_OUTPUT\"\n"
	if runtime.GOOS == "windows" {
		name, script = "bicep.cmd", "@echo off\r\ntype \"%AZD_TEST_BICEP_OUTPUT%\"\r\n"
	}
	tool := filepath.Join(dir, name)
	//nolint:gosec // local executable test fixture
	require.NoError(t, os.WriteFile(tool, []byte(script), 0o700))
	t.Setenv("AZD_BICEP_TOOL_PATH", tool)
	t.Setenv("AZD_TEST_BICEP_OUTPUT", result)
}

// Snapshot records all project files to detect preflight mutations.
func Snapshot(t testing.TB, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			files[path] = "<directory>"
		} else {
			//nolint:gosec // isolated test project
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = string(raw)
		}
		return nil
	})
	require.NoError(t, err)
	return files
}
