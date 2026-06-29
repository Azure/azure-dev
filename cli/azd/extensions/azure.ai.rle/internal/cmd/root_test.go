// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRootCommandIncludesExpectedCommands(t *testing.T) {
	rootCmd := NewRootCommand()

	for _, commandName := range []string{"deploy", "init", "invoke", "run", "version", "metadata"} {
		if command, _, err := rootCmd.Find([]string{commandName}); err != nil || command.Name() != commandName {
			t.Fatalf("expected command %q to be registered", commandName)
		}
	}
}

func TestDeployExposesProjectIdFlag(t *testing.T) {
	rootCmd := NewRootCommand()
	command, _, err := rootCmd.Find([]string{"deploy"})
	if err != nil {
		t.Fatalf("expected deploy command to be registered: %v", err)
	}
	if flag := command.Flags().Lookup("project-id"); flag == nil {
		t.Fatal("expected deploy to expose --project-id")
	}
}

func TestLifecycleFlagsAlignWithHostedAgentConventions(t *testing.T) {
	rootCmd := NewRootCommand()

	initCommand, _, err := rootCmd.Find([]string{"init"})
	if err != nil {
		t.Fatalf("expected init command to be registered: %v", err)
	}
	var initHelp bytes.Buffer
	initCommand.SetOut(&initHelp)
	if err := initCommand.Help(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(initHelp.String(), "--manifest") {
		t.Fatal("expected init help not to expose --manifest")
	}
	if !strings.Contains(initHelp.String(), "-m string") {
		t.Fatal("expected init help to expose -m")
	}

	runCommand, _, err := rootCmd.Find([]string{"run"})
	if err != nil {
		t.Fatalf("expected run command to be registered: %v", err)
	}
	if flag := runCommand.Flags().Lookup("port"); flag == nil {
		t.Fatal("expected run to expose --port")
	}

	invokeCommand, _, err := rootCmd.Find([]string{"invoke"})
	if err != nil {
		t.Fatalf("expected invoke command to be registered: %v", err)
	}
	if flag := invokeCommand.Flags().Lookup("timeout"); flag == nil {
		t.Fatal("expected invoke to expose --timeout")
	}
	if flag := invokeCommand.Flags().Lookup("local"); flag == nil {
		t.Fatal("expected invoke to expose --local")
	}
}

func TestInitParsesManifestShorthandOnly(t *testing.T) {
	if initUsedLongManifestFlag([]string{"init", "-m", "rle.yaml"}) {
		t.Fatal("expected -m to be allowed")
	}
	if !initUsedLongManifestFlag([]string{"init", "--manifest", "rle.yaml"}) {
		t.Fatal("expected --manifest to be detected")
	}
	if !initUsedLongManifestFlag([]string{"init", "--manifest=rle.yaml"}) {
		t.Fatal("expected --manifest=... to be detected")
	}
}

func TestLifecycleCommandsRejectPositionalArguments(t *testing.T) {
	rootCmd := NewRootCommand()

	for _, commandName := range []string{"deploy", "init", "invoke", "run"} {
		command, _, err := rootCmd.Find([]string{commandName})
		if err != nil {
			t.Fatalf("expected command %q to be registered: %v", commandName, err)
		}
		if err := command.Args(command, []string{"unexpected"}); err == nil {
			t.Fatalf("expected command %q to reject positional arguments", commandName)
		}
	}
}

func TestInitUsesEmbeddedEchoManifestByDefault(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	command := newInitCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	sessionDir := filepath.Join(tempDir, "echo_env")
	manifestBytes, err := os.ReadFile(filepath.Join(sessionDir, rleManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(manifestBytes); got != defaultEchoManifest {
		t.Fatalf("expected embedded echo manifest, got:\n%s", got)
	}

	stateBytes, err := os.ReadFile(filepath.Join(sessionDir, rleStateFile))
	if err != nil {
		t.Fatal(err)
	}
	var state rleState
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	if state.Name != "echo_env" {
		t.Fatalf("expected echo_env state name, got %q", state.Name)
	}
	if state.Image != "devrle.azurecr.io/echo-rl:latest" {
		t.Fatalf("expected echo image from manifest, got %q", state.Image)
	}
	if state.LocalImage != "devrle.azurecr.io/echo-rl:latest" {
		t.Fatalf("expected local image fallback from manifest, got %q", state.LocalImage)
	}
}

func TestInitInfersNameFromManifestOnly(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	manifestPath := filepath.Join(tempDir, "custom-rle.yaml")
	if err := os.WriteFile(manifestPath, []byte(`template:
  name: code_rl
  kind: openenv
  environment:
    image: example.azurecr.io/code:latest
`), 0600); err != nil {
		t.Fatal(err)
	}

	command := newInitCommand()
	command.SetArgs([]string{"-m", manifestPath})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(tempDir, "code_rl", rleStateFile)); err != nil {
		t.Fatal(err)
	}
}
