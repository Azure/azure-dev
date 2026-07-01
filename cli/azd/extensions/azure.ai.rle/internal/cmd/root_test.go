// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestRleUserCommandsHiddenUnlessEnabled(t *testing.T) {
	t.Setenv(rleEnableEnvVar, "")
	rootCmd := NewRootCommand()
	for _, commandName := range []string{"deploy", "init", "invoke", "run", "version"} {
		command, _, err := rootCmd.Find([]string{commandName})
		if err != nil {
			t.Fatalf("expected command %q to be registered: %v", commandName, err)
		}
		if !command.Hidden {
			t.Fatalf("expected command %q to be hidden when %s is not true", commandName, rleEnableEnvVar)
		}
	}
	metadataCommand, _, err := rootCmd.Find([]string{"metadata"})
	if err != nil {
		t.Fatalf("expected metadata command to be registered: %v", err)
	}
	if !metadataCommand.Hidden {
		t.Fatal("expected metadata command to remain hidden")
	}

	t.Setenv(rleEnableEnvVar, "true")
	rootCmd = NewRootCommand()
	for _, commandName := range []string{"deploy", "init", "invoke", "run", "version"} {
		command, _, err := rootCmd.Find([]string{commandName})
		if err != nil {
			t.Fatalf("expected command %q to be registered: %v", commandName, err)
		}
		if command.Hidden {
			t.Fatalf("expected command %q to be visible when %s=true", commandName, rleEnableEnvVar)
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
	if flag := command.Flags().Lookup("image"); flag != nil {
		t.Fatal("expected deploy not to expose --image")
	}
	if flag := command.Flags().Lookup("dockerfile"); flag == nil {
		t.Fatal("expected deploy to expose --dockerfile")
	}
	if flag := command.Flags().Lookup("name"); flag != nil {
		t.Fatal("expected deploy not to expose --name")
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
	if strings.Contains(initHelp.String(), "-m string") {
		t.Fatal("expected init help not to expose -m")
	}
	if flag := initCommand.Flags().Lookup("manifest"); flag != nil {
		t.Fatal("expected init not to expose --manifest")
	}
	if flag := initCommand.Flags().Lookup("name"); flag != nil {
		t.Fatal("expected init not to expose --name")
	}

	runCommand, _, err := rootCmd.Find([]string{"run"})
	if err != nil {
		t.Fatalf("expected run command to be registered: %v", err)
	}
	if flag := runCommand.Flags().Lookup("port"); flag == nil {
		t.Fatal("expected run to expose --port")
	}
	if flag := runCommand.Flags().Lookup("dockerfile"); flag == nil {
		t.Fatal("expected run to expose --dockerfile")
	}
	if flag := runCommand.Flags().Lookup("image"); flag != nil {
		t.Fatal("expected run not to expose --image")
	}
	if flag := runCommand.Flags().Lookup("watch"); flag == nil {
		t.Fatal("expected run to expose --watch")
	}
	if flag := runCommand.Flags().Lookup("source"); flag != nil {
		t.Fatal("expected run not to expose --source")
	}
	if flag := runCommand.Flags().Lookup("name"); flag != nil {
		t.Fatal("expected run not to expose --name")
	}

	invokeCommand, _, err := rootCmd.Find([]string{"invoke"})
	if err != nil {
		t.Fatalf("expected invoke command to be registered: %v", err)
	}
	if flag := invokeCommand.Flags().Lookup("timeout"); flag == nil {
		t.Fatal("expected invoke to expose --timeout")
	}
	if flag := invokeCommand.Flags().Lookup("local"); flag != nil {
		t.Fatal("expected invoke not to expose --local")
	}
	if flag := invokeCommand.Flags().Lookup("dockerfile"); flag != nil {
		t.Fatal("expected invoke not to expose --dockerfile")
	}
	if flag := invokeCommand.Flags().Lookup("image"); flag != nil {
		t.Fatal("expected invoke not to expose --image")
	}
	if flag := invokeCommand.Flags().Lookup("port"); flag != nil {
		t.Fatal("expected invoke not to expose --port")
	}
	if flag := invokeCommand.Flags().Lookup("source"); flag != nil {
		t.Fatal("expected invoke not to expose --source")
	}
	if flag := invokeCommand.Flags().Lookup("name"); flag != nil {
		t.Fatal("expected invoke not to expose --name")
	}
	if flag := invokeCommand.Flags().Lookup("endpoint"); flag != nil {
		t.Fatal("expected invoke not to expose --endpoint")
	}
}

func TestLifecycleCommandsRejectPositionalArguments(t *testing.T) {
	rootCmd := NewRootCommand()

	for _, commandName := range []string{"deploy", "invoke", "run"} {
		command, _, err := rootCmd.Find([]string{commandName})
		if err != nil {
			t.Fatalf("expected command %q to be registered: %v", commandName, err)
		}
		if err := command.Args(command, []string{"unexpected"}); err == nil {
			t.Fatalf("expected command %q to reject positional arguments", commandName)
		}
	}

	initCommand, _, err := rootCmd.Find([]string{"init"})
	if err != nil {
		t.Fatalf("expected init command to be registered: %v", err)
	}
	if err := initCommand.Args(initCommand, []string{"custom_env"}); err != nil {
		t.Fatalf("expected init to accept one positional environment name: %v", err)
	}
	if err := initCommand.Args(initCommand, []string{"one", "two"}); err == nil {
		t.Fatal("expected init to reject multiple positional arguments")
	}
}

func TestInitCopiesOpenEnvEchoSampleByDefault(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	stubOpenEnvEchoCheckout(t)

	command := newInitCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	sessionDir := filepath.Join(tempDir, "echo_env")
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
	if _, err := os.Stat(filepath.Join(sessionDir, "server", "Dockerfile")); err != nil {
		t.Fatalf("expected copied OpenEnv server Dockerfile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected copied sample not to include .git metadata, got err=%v", err)
	}
	if !strings.Contains(output.String(), fmt.Sprintf("cd %q", sessionDir)) {
		t.Fatalf("expected init output to quote cd path, got %s", output.String())
	}
}

func TestInitUsesPositionalNameForDefaultSample(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	stubOpenEnvEchoCheckout(t)

	command := newInitCommand()
	command.SetArgs([]string{"code_rl"})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	sessionDir := filepath.Join(tempDir, "code_rl")
	stateBytes, err := os.ReadFile(filepath.Join(sessionDir, rleStateFile))
	if err != nil {
		t.Fatal(err)
	}
	var state rleState
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	if state.Name != "code_rl" {
		t.Fatalf("expected code_rl state name, got %q", state.Name)
	}
}

func stubOpenEnvEchoCheckout(t *testing.T) {
	t.Helper()
	old := checkoutOpenEnvEchoSampleFunc
	checkoutOpenEnvEchoSampleFunc = func(name string, dest string, force bool) (string, error) {
		sessionDir, err := createRleSessionDir(name, dest, force)
		if err != nil {
			return "", err
		}
		serverDir := filepath.Join(sessionDir, "server")
		if err := os.MkdirAll(serverDir, 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(serverDir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(sessionDir, "openenv.yaml"), []byte("name: echo_env\n"), 0600); err != nil {
			return "", err
		}
		return sessionDir, nil
	}
	t.Cleanup(func() {
		checkoutOpenEnvEchoSampleFunc = old
	})
}
