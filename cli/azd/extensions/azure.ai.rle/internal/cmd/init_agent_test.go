// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestRleInitTypeOptionsGateAgentChoices(t *testing.T) {
	withoutAgents := rleInitTypeOptions(false)
	if len(withoutAgents) != 1 ||
		withoutAgents[0].initType != rleInitTypeGymOpenEnv ||
		withoutAgents[0].label != "Gym, OpenEnv" {
		t.Fatalf("expected only Gym/OpenEnv by default, got %#v", withoutAgents)
	}

	withAgents := rleInitTypeOptions(true)
	if len(withAgents) != 3 {
		t.Fatalf("expected three init choices with agent preview enabled, got %#v", withAgents)
	}
	if withAgents[1].initType != rleInitTypeHostedAgent || withAgents[1].label != "Agent, Hosted Agent" {
		t.Fatalf("unexpected Hosted Agent choice: %#v", withAgents[1])
	}
	if withAgents[2].initType != rleInitTypeBYOH || withAgents[2].label != "Agent, BYOH" {
		t.Fatalf("unexpected BYOH choice: %#v", withAgents[2])
	}
}

func TestInitInteractiveHostedAgentScaffoldsUsingPromptedValues(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleAgentInitEnableEnvVar, "true")

	oldSelectType := selectRleInitTypeFunc
	oldPrompt := promptRleValueFunc
	oldLoadCatalog := loadRleSampleCatalogFunc
	selectRleInitTypeFunc = func(_ context.Context, includeAgentTypes bool) (rleInitType, error) {
		if !includeAgentTypes {
			t.Fatal("expected Hosted Agent selection to be enabled")
		}
		return rleInitTypeHostedAgent, nil
	}
	promptValues := []string{"Support Agent", "v3"}
	promptRleValueFunc = func(_ context.Context, _ string) (string, error) {
		if len(promptValues) == 0 {
			t.Fatal("unexpected extra input prompt")
		}
		value := promptValues[0]
		promptValues = promptValues[1:]
		return value, nil
	}
	loadRleSampleCatalogFunc = func() (rleSampleCatalog, error) {
		t.Fatal("agent initialization must not load the sample catalog")
		return nil, nil
	}
	t.Cleanup(func() {
		selectRleInitTypeFunc = oldSelectType
		promptRleValueFunc = oldPrompt
		loadRleSampleCatalogFunc = oldLoadCatalog
	})

	noPrompt := false
	command := newInitCommand(&noPrompt)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs(nil)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(promptValues) != 0 {
		t.Fatalf("expected all Hosted Agent prompts to be consumed, remaining %v", promptValues)
	}

	sessionDir := filepath.Join(tempDir, "support_agent")
	config, err := os.ReadFile(filepath.Join(sessionDir, "rle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`name = "support_agent"`,
		`kind = "hosted_agent"`,
		`name = "Support Agent"`,
		`version = "v3"`,
	} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("expected config to contain %q, got:\n%s", expected, config)
		}
	}
	if !strings.Contains(output.String(), "Created Hosted Agent RLE scaffold.") {
		t.Fatalf("expected Hosted Agent scaffold confirmation, got %s", output.String())
	}
	stateBytes, err := os.ReadFile(filepath.Join(sessionDir, rleStateFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stateBytes), `"environmentName": "support_agent"`) {
		t.Fatalf("expected initialized RLE state, got:\n%s", stateBytes)
	}
}

func TestInitInteractiveBYOHScaffoldsUsingPromptedValues(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleAgentInitEnableEnvVar, "true")

	oldSelectType := selectRleInitTypeFunc
	oldPrompt := promptRleValueFunc
	selectRleInitTypeFunc = func(_ context.Context, includeAgentTypes bool) (rleInitType, error) {
		if !includeAgentTypes {
			t.Fatal("expected BYOH selection to be enabled")
		}
		return rleInitTypeBYOH, nil
	}
	promptValues := []string{"customer_rle", "https://agent.example.com/v1/"}
	promptRleValueFunc = func(_ context.Context, _ string) (string, error) {
		value := promptValues[0]
		promptValues = promptValues[1:]
		return value, nil
	}
	t.Cleanup(func() {
		selectRleInitTypeFunc = oldSelectType
		promptRleValueFunc = oldPrompt
	})

	noPrompt := false
	command := newInitCommand(&noPrompt)
	command.SetArgs(nil)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	config, err := os.ReadFile(filepath.Join(tempDir, "customer_rle", "rle.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `base_url = "https://agent.example.com/v1"`) {
		t.Fatalf("expected normalized BYOH base URL, got:\n%s", config)
	}
}

func TestInitNoPromptHostedAgentUsesExplicitFlags(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleAgentInitEnableEnvVar, "true")

	noPrompt := true
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{
		"support_rle",
		"--type", "hosted-agent",
		"--agent-name", "support-agent",
		"--agent-version", "2026.09",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "support_rle", "server", "env.py")); err != nil {
		t.Fatalf("expected Hosted Agent scaffold to be created: %v", err)
	}
}

func TestInitNoPromptBYOHRequiresFolderName(t *testing.T) {
	t.Setenv(rleAgentInitEnableEnvVar, "true")

	noPrompt := true
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{"--type", "byoh", "--base-url", "https://agent.example.com"})
	err := command.Execute()
	localError, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localError.Code != "rle_environment_name_required" {
		t.Fatalf("expected missing BYOH folder name error, got %v", err)
	}
}

func TestInitAgentTypeRequiresPreviewFlag(t *testing.T) {
	t.Setenv(rleAgentInitEnableEnvVar, "")

	noPrompt := true
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{
		"support_rle",
		"--type", "hosted-agent",
		"--agent-name", "support-agent",
		"--agent-version", "v1",
	})
	err := command.Execute()
	localError, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localError.Code != "rle_agent_init_disabled" {
		t.Fatalf("expected disabled agent init error, got %v", err)
	}
}

func TestInitSelectedAgentTypeRequiresPreviewFlag(t *testing.T) {
	t.Setenv(rleAgentInitEnableEnvVar, "")

	oldSelectType := selectRleInitTypeFunc
	selectRleInitTypeFunc = func(_ context.Context, includeAgentTypes bool) (rleInitType, error) {
		if includeAgentTypes {
			t.Fatal("expected agent init choices to be disabled")
		}
		return rleInitTypeHostedAgent, nil
	}
	t.Cleanup(func() {
		selectRleInitTypeFunc = oldSelectType
	})

	noPrompt := false
	command := newInitCommand(&noPrompt)
	err := command.Execute()
	localError, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localError.Code != "rle_agent_init_disabled" {
		t.Fatalf("expected disabled selected agent type error, got %v", err)
	}
}
