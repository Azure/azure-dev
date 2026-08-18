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

func TestLoadRleStateAcceptsLegacyName(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	if err := os.WriteFile(
		rleStateFile,
		[]byte(`{"name":"legacy_env","projectEndpoint":"https://example.test/project"}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	state, err := loadRleState()
	if err != nil {
		t.Fatal(err)
	}
	if state.EnvironmentName != "legacy_env" {
		t.Fatalf("expected legacy environment name, got %q", state.EnvironmentName)
	}
	if state.ProjectEndpoint != "https://example.test/project" {
		t.Fatalf("expected project endpoint, got %q", state.ProjectEndpoint)
	}
}

func TestLoadRleStatePrefersEnvironmentName(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	if err := os.WriteFile(
		rleStateFile,
		[]byte(`{"environmentName":"current_env","name":"legacy_env"}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}

	state, err := loadRleState()
	if err != nil {
		t.Fatal(err)
	}
	if state.EnvironmentName != "current_env" {
		t.Fatalf("expected current environment name, got %q", state.EnvironmentName)
	}
}
