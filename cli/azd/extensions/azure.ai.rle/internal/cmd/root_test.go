// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleHelpMatchesSupportedArguments(t *testing.T) {
	command := newRolloutCommand()
	if command.Use != "rollout" {
		t.Fatalf("expected manifest-based rollout usage, got %q", command.Use)
	}
	examples := strings.Split(command.Example, "\n")
	metadata, err := os.ReadFile(filepath.Join("..", "..", "extension.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	foundMetadataExample := false
	for line := range strings.SplitSeq(string(metadata), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "usage: azd ai rle rollout ") {
			examples = append(examples, strings.TrimPrefix(line, "usage: "))
			foundMetadataExample = true
		}
	}
	if !foundMetadataExample {
		t.Fatal("expected an extension metadata example for rollout")
	}
	for _, example := range examples {
		args := strings.Fields(strings.TrimSpace(example))
		if len(args) < 4 {
			t.Fatalf("expected a full rollout command, got %q", example)
		}
		parsed := newRolloutCommand()
		if err := parsed.ParseFlags(args[4:]); err != nil {
			t.Fatalf("unsupported rollout example %q: %v", example, err)
		}
		if err := parsed.Args(parsed, parsed.Flags().Args()); err != nil {
			t.Fatalf("unsupported positional argument in %q: %v", example, err)
		}
	}
	publish := newPublishCommand()
	if strings.Contains(publish.Flags().Lookup("task-dir").Usage, "--task-name") {
		t.Fatal("publish help must not advertise an unsupported rollout flag")
	}
}

func TestNewRootCommandIncludesExpectedCommands(t *testing.T) {
	rootCmd := NewRootCommand()

	for _, commandName := range []string{
		"list", "models", "show", "rollout", "monitor", "publish", "run", "version", "metadata", "jobs", "train",
	} {
		if command, _, err := rootCmd.Find([]string{commandName}); err != nil || command.Name() != commandName {
			t.Fatalf("expected command %q to be registered", commandName)
		}
	}
	for _, commandName := range []string{"environments", "deploy", "cancel"} {
		if command, _, err := rootCmd.Find([]string{commandName}); err == nil && command.Name() == commandName {
			t.Fatalf("expected no %s alias", commandName)
		}
	}
}

func TestJobsIncludesCancelWithPositionalJobID(t *testing.T) {
	rootCmd := NewRootCommand()
	command, _, err := rootCmd.Find([]string{"jobs", "cancel"})
	if err != nil || command.Name() != "cancel" || command.Parent().Name() != "jobs" {
		t.Fatalf("expected jobs cancel to be registered: %v", err)
	}
	if command.Use != "cancel <job-id>" {
		t.Fatalf("unexpected cancel usage: %q", command.Use)
	}
	if command.Flags().Lookup("job-id") != nil {
		t.Fatal("expected cancel to take a positional job ID, not --job-id")
	}
	if err := command.Args(command, []string{realMonitorJobID}); err != nil {
		t.Fatalf("expected cancel to accept a job ID: %v", err)
	}
}

func TestRleUserCommandsVisibleByDefault(t *testing.T) {
	rootCmd := NewRootCommand()
	for _, commandName := range []string{"list", "models", "show", "rollout", "monitor", "publish", "run", "version"} {
		command, _, err := rootCmd.Find([]string{commandName})
		if err != nil {
			t.Fatalf("expected command %q to be registered: %v", commandName, err)
		}
		if command.Hidden {
			t.Fatalf("expected command %q to be visible without any opt-in flag", commandName)
		}
	}
	metadataCommand, _, err := rootCmd.Find([]string{"metadata"})
	if err != nil {
		t.Fatalf("expected metadata command to be registered: %v", err)
	}
	if !metadataCommand.Hidden {
		t.Fatal("expected metadata command to remain hidden")
	}
}

func TestInternalCommandsHiddenUnlessEnableAll(t *testing.T) {
	for _, commandName := range []string{"jobs", "train"} {
		t.Run(commandName, func(t *testing.T) {
			t.Setenv(rleEnableAllEnvVar, "")
			rootCmd := NewRootCommand()
			command, _, err := rootCmd.Find([]string{commandName})
			if err != nil {
				t.Fatalf("expected %s command to be registered: %v", commandName, err)
			}
			if !command.Hidden {
				t.Fatalf("expected %s to be hidden unless %s=true", commandName, rleEnableAllEnvVar)
			}

			t.Setenv(rleEnableAllEnvVar, "true")
			rootCmd = NewRootCommand()
			command, _, err = rootCmd.Find([]string{commandName})
			if err != nil {
				t.Fatalf("expected %s command to be registered: %v", commandName, err)
			}
			if command.Hidden {
				t.Fatalf("expected %s to be visible when %s=true", commandName, rleEnableAllEnvVar)
			}
		})
	}
}

func TestPublishExposesStandaloneFlags(t *testing.T) {
	rootCmd := NewRootCommand()
	command, _, err := rootCmd.Find([]string{"publish"})
	if err != nil {
		t.Fatalf("expected publish command to be registered: %v", err)
	}
	for _, name := range []string{"project-endpoint", "project-id", "image", "version-bump", "name"} {
		if flag := command.Flags().Lookup(name); flag != nil {
			t.Fatalf("expected publish not to expose --%s", name)
		}
	}
	if flag := command.Flags().Lookup("dockerfile"); flag == nil {
		t.Fatal("expected publish to expose --dockerfile")
	}
}

func TestLifecycleFlagsAlignWithHostedAgentConventions(t *testing.T) {
	rootCmd := NewRootCommand()
	runCommand, _, err := rootCmd.Find([]string{"run"})
	if err != nil {
		t.Fatalf("expected run command to be registered: %v", err)
	}
	for _, name := range []string{"port", "dockerfile", "watch"} {
		if flag := runCommand.Flags().Lookup(name); flag == nil {
			t.Fatalf("expected run to expose --%s", name)
		}
	}
	for _, name := range []string{"image", "source", "name"} {
		if flag := runCommand.Flags().Lookup(name); flag != nil {
			t.Fatalf("expected run not to expose --%s", name)
		}
	}

	rolloutCommand, _, err := rootCmd.Find([]string{"rollout"})
	if err != nil {
		t.Fatalf("expected rollout command to be registered: %v", err)
	}
	for _, name := range []string{
		"timeout", "version", "local", "dockerfile", "image", "port", "source", "name", "endpoint",
	} {
		if flag := rolloutCommand.Flags().Lookup(name); flag != nil {
			t.Fatalf("expected rollout not to expose --%s", name)
		}
	}
}

func TestLifecycleCommandsRejectPositionalArguments(t *testing.T) {
	rootCmd := NewRootCommand()
	for _, commandName := range []string{"publish", "run", "list", "rollout"} {
		command, _, err := rootCmd.Find([]string{commandName})
		if err != nil {
			t.Fatalf("expected command %q to be registered: %v", commandName, err)
		}
		if err := command.Args(command, []string{"unexpected"}); err == nil {
			t.Fatalf("expected command %q to reject positional arguments", commandName)
		}
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
}
