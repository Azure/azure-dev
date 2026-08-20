// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSaveRleStateWritesEnvironmentName(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	if err := saveRleState(rleState{EnvironmentName: "echo_env"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(rleStateFile)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["environmentName"] != "echo_env" {
		t.Fatalf("expected environmentName, got %#v", persisted)
	}
	if _, exists := persisted["name"]; exists {
		t.Fatalf("expected legacy name to be omitted, got %#v", persisted)
	}
}

func TestRleStateUnmarshalNameCompatibility(t *testing.T) {
	for _, test := range []struct {
		name     string
		payload  string
		expected string
	}{
		{
			name:     "accepts legacy name",
			payload:  `{"name":"legacy_env"}`,
			expected: "legacy_env",
		},
		{
			name:     "prefers environment name",
			payload:  `{"environmentName":"current_env","name":"legacy_env"}`,
			expected: "current_env",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var state rleState
			if err := json.Unmarshal([]byte(test.payload), &state); err != nil {
				t.Fatal(err)
			}
			if state.EnvironmentName != test.expected {
				t.Fatalf("expected %q, got %q", test.expected, state.EnvironmentName)
			}
		})
	}
}
