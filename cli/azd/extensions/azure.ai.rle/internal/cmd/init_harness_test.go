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

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestRleInitTargetOptionsGateHarnessChoices(t *testing.T) {
	withoutHarnesses := rleInitTargetOptions(false)
	if len(withoutHarnesses) != 1 ||
		withoutHarnesses[0].target != gymOpenEnvInitTarget ||
		withoutHarnesses[0].label != "Gym: OpenEnv" {
		t.Fatalf("expected only Gym: OpenEnv by default, got %#v", withoutHarnesses)
	}

	withHarnesses := rleInitTargetOptions(true)
	if len(withHarnesses) != 3 {
		t.Fatalf("expected three init choices with harness preview enabled, got %#v", withHarnesses)
	}
	if withHarnesses[1].target != (rleInitTarget{
		rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent,
	}) || withHarnesses[1].label != "Harness: HostedAgent" {
		t.Fatalf("unexpected HostedAgent choice: %#v", withHarnesses[1])
	}
	if withHarnesses[2].target != (rleInitTarget{
		rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeBYOH,
	}) || withHarnesses[2].label != "Harness: BYOH" {
		t.Fatalf("unexpected BYOH choice: %#v", withHarnesses[2])
	}
}

func TestInitInteractiveHostedAgentScaffoldsUsingPromptedValues(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleHarnessInitEnableEnvVar, "true")

	oldSelectTarget := selectRleInitTargetFunc
	oldPrompt := promptRleValueFunc
	oldLoadCatalog := loadRleSampleCatalogFunc
	selectRleInitTargetFunc = func(_ context.Context, includeHarnessTypes bool) (rleInitTarget, error) {
		if !includeHarnessTypes {
			t.Fatal("expected HostedAgent selection to be enabled")
		}
		return rleInitTarget{
			rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent,
		}, nil
	}
	promptValues := []string{"Support Agent", "3"}
	promptRleValueFunc = func(_ context.Context, _ string) (string, error) {
		if len(promptValues) == 0 {
			t.Fatal("unexpected extra input prompt")
		}
		value := promptValues[0]
		promptValues = promptValues[1:]
		return value, nil
	}
	loadRleSampleCatalogFunc = func() (rleSampleCatalog, error) {
		t.Fatal("harness initialization must not load the sample catalog")
		return nil, nil
	}
	t.Cleanup(func() {
		selectRleInitTargetFunc = oldSelectTarget
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
		t.Fatalf("expected all HostedAgent prompts to be consumed, remaining %v", promptValues)
	}

	sessionDir := filepath.Join(tempDir, "support_agent")
	config, err := os.ReadFile(filepath.Join(sessionDir, project.RleConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`schema_version = '1.0.0'`,
		`name = 'support_agent'`,
		`version = '1.0.0'`,
		`type = 'Harness'`,
		`subtype = 'HostedAgent'`,
		`agentName = 'Support Agent'`,
		`agentVersion = '3'`,
	} {
		if !strings.Contains(string(config), expected) {
			t.Fatalf("expected config to contain %q, got:\n%s", expected, config)
		}
	}
	if !strings.Contains(output.String(), "Created HostedAgent RLE scaffold.") {
		t.Fatalf("expected HostedAgent scaffold confirmation, got %s", output.String())
	}
	if _, err := os.Stat(filepath.Join(sessionDir, ".azd-rle.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no legacy state file, got %v", err)
	}
}

func TestInitInteractiveBYOHScaffoldsUsingPromptedValues(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleHarnessInitEnableEnvVar, "true")

	oldSelectTarget := selectRleInitTargetFunc
	oldPrompt := promptRleValueFunc
	selectRleInitTargetFunc = func(_ context.Context, includeHarnessTypes bool) (rleInitTarget, error) {
		if !includeHarnessTypes {
			t.Fatal("expected BYOH selection to be enabled")
		}
		return rleInitTarget{
			rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeBYOH,
		}, nil
	}
	promptValues := []string{"customer_rle", "https://harness.example.com/v1/"}
	promptRleValueFunc = func(_ context.Context, _ string) (string, error) {
		value := promptValues[0]
		promptValues = promptValues[1:]
		return value, nil
	}
	t.Cleanup(func() {
		selectRleInitTargetFunc = oldSelectTarget
		promptRleValueFunc = oldPrompt
	})

	noPrompt := false
	command := newInitCommand(&noPrompt)
	command.SetArgs(nil)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	config, err := os.ReadFile(filepath.Join(tempDir, "customer_rle", project.RleConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `baseUrl = 'https://harness.example.com/v1/'`) {
		t.Fatalf("expected normalized BYOH base URL, got:\n%s", config)
	}
}

func TestInitNoPromptHostedAgentUsesControlPlaneFlags(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(rleHarnessInitEnableEnvVar, "true")

	noPrompt := true
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{
		"support_rle",
		"--type", "Harness",
		"--subtype", "HostedAgent",
		"--rle-version", "1.0.0",
		"--agent-name", "support-agent",
		"--agent-version", "202609",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "support_rle", "server", "env.py")); err != nil {
		t.Fatalf("expected HostedAgent scaffold to be created: %v", err)
	}
}

func TestInitNoPromptBYOHRequiresFolderName(t *testing.T) {
	t.Setenv(rleHarnessInitEnableEnvVar, "true")

	noPrompt := true
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{
		"--type", "Harness",
		"--subtype", "BYOH",
		"--base-url", "https://harness.example.com",
	})
	err := command.Execute()
	localError, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localError.Code != "rle_environment_name_required" {
		t.Fatalf("expected missing BYOH folder name error, got %v", err)
	}
}

func TestInitHarnessTargetRequiresPreviewFlag(t *testing.T) {
	t.Setenv(rleHarnessInitEnableEnvVar, "")

	noPrompt := true
	command := newInitCommand(&noPrompt)
	command.SetArgs([]string{
		"support_rle",
		"--type", "Harness",
		"--subtype", "HostedAgent",
		"--agent-name", "support-agent",
		"--agent-version", "1",
	})
	err := command.Execute()
	localError, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localError.Code != "rle_harness_init_disabled" {
		t.Fatalf("expected disabled harness init error, got %v", err)
	}
}

func TestInitSelectedHarnessTargetRequiresPreviewFlag(t *testing.T) {
	t.Setenv(rleHarnessInitEnableEnvVar, "")

	oldSelectTarget := selectRleInitTargetFunc
	selectRleInitTargetFunc = func(_ context.Context, includeHarnessTypes bool) (rleInitTarget, error) {
		if includeHarnessTypes {
			t.Fatal("expected harness init choices to be disabled")
		}
		return rleInitTarget{
			rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent,
		}, nil
	}
	t.Cleanup(func() {
		selectRleInitTargetFunc = oldSelectTarget
	})

	noPrompt := false
	command := newInitCommand(&noPrompt)
	err := command.Execute()
	localError, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localError.Code != "rle_harness_init_disabled" {
		t.Fatalf("expected disabled selected harness type error, got %v", err)
	}
}

func TestParseRleInitTargetUsesControlPlanePairs(t *testing.T) {
	tests := []struct {
		name      string
		rleType   string
		subtype   string
		expected  rleInitTarget
		errorCode string
	}{
		{
			name:     "gym defaults to openenv",
			rleType:  "Gym",
			expected: gymOpenEnvInitTarget,
		},
		{
			name:     "hosted agent harness",
			rleType:  "Harness",
			subtype:  "HostedAgent",
			expected: rleInitTarget{rleType: project.RleTypeHarness, rleSubtype: project.RleSubtypeHostedAgent},
		},
		{
			name:      "harness needs subtype",
			rleType:   "Harness",
			errorCode: "rle_init_subtype_required",
		},
		{
			name:      "invalid pair",
			rleType:   "Gym",
			subtype:   "BYOH",
			errorCode: "rle_init_type_configuration_invalid",
		},
		{
			name:      "legacy agent type is rejected",
			rleType:   "Agent",
			subtype:   "HostedAgent",
			errorCode: "rle_init_type_invalid",
		},
		{
			name:      "legacy byoa subtype is rejected",
			rleType:   "Harness",
			subtype:   "BYOA",
			errorCode: "rle_init_subtype_invalid",
		},
		{
			name:      "legacy combined target is rejected",
			rleType:   "hosted-agent",
			errorCode: "rle_init_type_invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := parseRleInitTarget(test.rleType, test.subtype)
			if test.errorCode == "" {
				if err != nil {
					t.Fatal(err)
				}
				if target != test.expected {
					t.Fatalf("expected %#v, got %#v", test.expected, target)
				}
				return
			}
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			if !ok || localErr.Code != test.errorCode {
				t.Fatalf("expected %s, got %v", test.errorCode, err)
			}
		})
	}
}
