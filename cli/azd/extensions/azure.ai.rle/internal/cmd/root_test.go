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

	for _, commandName := range []string{"list", "show", "init", "invoke", "publish", "run", "version", "metadata"} {
		if command, _, err := rootCmd.Find([]string{commandName}); err != nil || command.Name() != commandName {
			t.Fatalf("expected command %q to be registered", commandName)
		}
	}
	if command, _, err := rootCmd.Find([]string{"environments"}); err == nil && command.Name() == "environments" {
		t.Fatal("expected no environments alias")
	}
	if command, _, err := rootCmd.Find([]string{"deploy"}); err == nil && command.Name() == "deploy" {
		t.Fatal("expected no deploy alias")
	}
}

func TestRleUserCommandsHiddenUnlessEnabled(t *testing.T) {
	t.Setenv(rleEnableEnvVar, "")
	rootCmd := NewRootCommand()
	for _, commandName := range []string{"list", "show", "init", "invoke", "publish", "run"} {
		command, _, err := rootCmd.Find([]string{commandName})
		if err != nil {
			t.Fatalf("expected command %q to be registered: %v", commandName, err)
		}
		if !command.Hidden {
			t.Fatalf("expected command %q to be hidden when %s is not true", commandName, rleEnableEnvVar)
		}
	}
	versionCommand, _, err := rootCmd.Find([]string{"version"})
	if err != nil {
		t.Fatalf("expected version command to be registered: %v", err)
	}
	if versionCommand.Hidden {
		t.Fatal("expected version command to remain visible when preview lifecycle commands are hidden")
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
	for _, commandName := range []string{"list", "show", "init", "invoke", "publish", "run", "version"} {
		command, _, err := rootCmd.Find([]string{commandName})
		if err != nil {
			t.Fatalf("expected command %q to be registered: %v", commandName, err)
		}
		if command.Hidden {
			t.Fatalf("expected command %q to be visible when %s=true", commandName, rleEnableEnvVar)
		}
	}
}

func TestPublishExposesStandaloneFlags(t *testing.T) {
	rootCmd := NewRootCommand()
	command, _, err := rootCmd.Find([]string{"publish"})
	if err != nil {
		t.Fatalf("expected publish command to be registered: %v", err)
	}
	if flag := command.Flags().Lookup("project-endpoint"); flag != nil {
		t.Fatal("expected publish not to expose --project-endpoint")
	}
	if flag := command.Flags().Lookup("project-id"); flag != nil {
		t.Fatal("expected publish not to expose --project-id")
	}
	if flag := command.Flags().Lookup("image"); flag != nil {
		t.Fatal("expected publish not to expose --image")
	}
	if flag := command.Flags().Lookup("dockerfile"); flag == nil {
		t.Fatal("expected publish to expose --dockerfile")
	}
	if flag := command.Flags().Lookup("version-bump"); flag == nil {
		t.Fatal("expected publish to expose --version-bump")
	} else if got := flag.DefValue; got != "major" {
		t.Fatalf("expected --version-bump default to be major, got %q", got)
	}
	if flag := command.Flags().Lookup("name"); flag != nil {
		t.Fatal("expected publish not to expose --name")
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
	if flag := invokeCommand.Flags().Lookup("version"); flag == nil {
		t.Fatal("expected invoke to expose --version")
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

	for _, commandName := range []string{"publish", "run"} {
		command, _, err := rootCmd.Find([]string{commandName})
		if err != nil {
			t.Fatalf("expected command %q to be registered: %v", commandName, err)
		}

		listCommand, _, err := rootCmd.Find([]string{"list"})
		if err != nil {
			t.Fatalf("expected list command to be registered: %v", err)
		}
		if err := listCommand.Args(listCommand, []string{"unexpected"}); err == nil {
			t.Fatal("expected list to reject positional arguments")
		}
		showCommand, _, err := rootCmd.Find([]string{"show"})
		if err != nil {
			t.Fatalf("expected show command to be registered: %v", err)
		}
		if err := showCommand.Args(showCommand, []string{"unexpected", "extra"}); err == nil {
			t.Fatal("expected show to reject multiple positional arguments")
		}
		if err := command.Args(command, []string{"unexpected"}); err == nil {
			t.Fatalf("expected command %q to reject positional arguments", commandName)
		}
	}

	invokeCommand, _, err := rootCmd.Find([]string{"invoke"})
	if err != nil {
		t.Fatalf("expected invoke command to be registered: %v", err)
	}
	if err := invokeCommand.Args(invokeCommand, []string{"code_rl"}); err != nil {
		t.Fatalf("expected invoke to accept one environment name: %v", err)
	}
	if err := invokeCommand.Args(invokeCommand, []string{"one", "two"}); err == nil {
		t.Fatal("expected invoke to reject multiple environment names")
	}

	showCommand, _, err := rootCmd.Find([]string{"show"})
	if err != nil {
		t.Fatalf("expected show command to be registered: %v", err)
	}
	if err := showCommand.Args(showCommand, []string{"code_rl"}); err != nil {
		t.Fatalf("expected show to accept one environment name: %v", err)
	}
	if err := showCommand.Args(showCommand, []string{"one", "two"}); err == nil {
		t.Fatal("expected show to reject multiple environment names")
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
	stateBytes, err := os.ReadFile(filepath.Join(sessionDir, rleStateFile)) //nolint:gosec // test reads the state file from its own temporary session directory.
	if err != nil {
		t.Fatal(err)
	}
	var state rleState
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	if state.EnvironmentName != "echo_env" {
		t.Fatalf("expected echo_env environment name, got %q", state.EnvironmentName)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "server", "Dockerfile")); err != nil {
		t.Fatalf("expected copied OpenEnv server Dockerfile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected copied sample not to include .git metadata, got err=%v", err)
	}
	if strings.Contains(output.String(), sessionDir) {
		t.Fatalf("expected init output not to use absolute cd path, got %s", output.String())
	}
	expectedCd := `cd "` + "." + string(os.PathSeparator) + "echo_env" + `"`
	if !strings.Contains(output.String(), expectedCd) {
		t.Fatalf("expected init output to quote relative cd path, got %s", output.String())
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
	stateBytes, err := os.ReadFile(filepath.Join(sessionDir, rleStateFile)) //nolint:gosec // test reads the state file from its own temporary session directory.
	if err != nil {
		t.Fatal(err)
	}
	var state rleState
	if err := json.Unmarshal(stateBytes, &state); err != nil {
		t.Fatal(err)
	}
	if state.EnvironmentName != "code_rl" {
		t.Fatalf("expected code_rl environment name, got %q", state.EnvironmentName)
	}
}

func TestInitNextStepsUseShellAppropriateSyntax(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		shell      string
		expected   string
		unexpected string
	}{
		{
			name:       "PowerShell on Windows",
			goos:       "windows",
			shell:      "pwsh.exe",
			expected:   `$env:FOUNDRY_PROJECT_ENDPOINT`,
			unexpected: `export FOUNDRY_PROJECT_ENDPOINT`,
		},
		{
			name:       "PowerShell on Linux",
			goos:       "linux",
			shell:      "/usr/bin/pwsh",
			expected:   `$env:FOUNDRY_PROJECT_ENDPOINT`,
			unexpected: `export FOUNDRY_PROJECT_ENDPOINT`,
		},
		{
			name:       "Bash on Linux or WSL",
			goos:       "linux",
			shell:      "/bin/bash",
			expected:   `export FOUNDRY_PROJECT_ENDPOINT`,
			unexpected: `$env:FOUNDRY_PROJECT_ENDPOINT`,
		},
		{
			name:       "Bash on Windows",
			goos:       "windows",
			shell:      "C:\\Program Files\\Git\\bin\\bash.exe",
			expected:   `export FOUNDRY_PROJECT_ENDPOINT`,
			unexpected: `$env:FOUNDRY_PROJECT_ENDPOINT`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := initNextSteps("./echo", test.goos, test.shell)
			for _, expected := range []string{
				"Run locally:",
				"azd ai rle run",
				"Publish to RLE when ready:",
				"azd ai rle publish",
				test.expected,
			} {
				if !strings.Contains(output, expected) {
					t.Fatalf("expected output to contain %q, got %s", expected, output)
				}
			}
			if strings.Contains(output, test.unexpected) || strings.Contains(output, "azd ai rle deploy") {
				t.Fatalf("unexpected shell or obsolete command guidance: %s", output)
			}
		})
	}
}

func stubOpenEnvEchoCheckout(t *testing.T) {
	t.Helper()
	old := checkoutOpenEnvEchoSampleFunc
	checkoutOpenEnvEchoSampleFunc = func(name string, dest string, force bool) (string, error) {
		sessionDir := filepath.Join(dest, name)
		if force {
			if err := os.RemoveAll(sessionDir); err != nil {
				return "", err
			}
		}
		if err := os.MkdirAll(sessionDir, 0750); err != nil {
			return "", err
		}
		serverDir := filepath.Join(sessionDir, "server")
		if err := os.MkdirAll(serverDir, 0750); err != nil {
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
