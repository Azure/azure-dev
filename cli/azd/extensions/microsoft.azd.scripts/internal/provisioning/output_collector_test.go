// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutputCollector_CollectsOutputs(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts")
	require.NoError(t, os.MkdirAll(scriptDir, 0o755))

	outputs := map[string]OutputParameter{
		"WEBSITE_URL":    {Type: "string", Value: "https://myapp.azurewebsites.net"},
		"RESOURCE_GROUP": {Type: "string", Value: "rg-myapp-dev"},
	}
	data, err := json.Marshal(outputs)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "outputs.json"), data, 0o600))

	// Create a dummy script so the config references work
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "setup.sh"), []byte("#!/bin/bash"), 0o600))

	collector := NewOutputCollector(dir)
	sc := &ScriptConfig{Run: "scripts/setup.sh"}

	result, err := collector.Collect(sc)
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "https://myapp.azurewebsites.net", result["WEBSITE_URL"].Value)
	require.Equal(t, "rg-myapp-dev", result["RESOURCE_GROUP"].Value)
}

func TestOutputCollector_NoOutputsFile(t *testing.T) {
	dir := t.TempDir()
	scriptDir := filepath.Join(dir, "scripts")
	require.NoError(t, os.MkdirAll(scriptDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "setup.sh"), []byte("#!/bin/bash"), 0o600))

	collector := NewOutputCollector(dir)
	sc := &ScriptConfig{Run: "scripts/setup.sh"}

	result, err := collector.Collect(sc)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestMergeOutputs(t *testing.T) {
	a := map[string]OutputParameter{"X": {Value: "a"}}
	b := map[string]OutputParameter{"X": {Value: "b"}, "Y": {Value: "c"}}

	merged := MergeOutputs(a, b)
	require.Equal(t, "b", merged["X"].Value, "later overrides earlier")
	require.Equal(t, "c", merged["Y"].Value)
}

func TestOutputsToEnvMap(t *testing.T) {
	outputs := map[string]OutputParameter{
		"KEY1": {Type: "string", Value: "val1"},
		"KEY2": {Type: "string", Value: "val2"},
	}

	env := OutputsToEnvMap(outputs)
	require.Equal(t, "val1", env["KEY1"])
	require.Equal(t, "val2", env["KEY2"])
}
