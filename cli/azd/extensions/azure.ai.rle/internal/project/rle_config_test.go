// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestWriteAndLoadRleConfigCanonicalizesHostedAgentManifest(t *testing.T) {
	dir := t.TempDir()
	agentName := " support-agent "
	agentVersion := "12"
	schemaVersion := CurrentRleManifestSchemaVersion
	config := RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: RleManifest{
			Name:         "support_agent",
			Version:      "1.0.0",
			Type:         RleTypeHarness,
			Subtype:      RleSubtypeHostedAgent,
			AgentName:    &agentName,
			AgentVersion: &agentVersion,
		},
	}

	if err := WriteRleConfig(dir, config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, RleConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`schema_version = '1.0.0'`,
		`name = 'support_agent'`,
		`version = '1.0.0'`,
		`type = 'Harness'`,
		`subtype = 'HostedAgent'`,
		`agentName = 'support-agent'`,
		`agentVersion = '12'`,
	} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("expected config to contain %q, got:\n%s", expected, data)
		}
	}
	if strings.Contains(string(data), "kind") {
		t.Fatalf("expected canonical type/subtype fields, got:\n%s", data)
	}
	if schemaIndex, rleIndex := strings.Index(string(data), "schema_version"), strings.Index(string(data), "[rle]"); schemaIndex < 0 || rleIndex < 0 || schemaIndex > rleIndex {
		t.Fatalf("expected schema_version to precede the [rle] table, got:\n%s", data)
	}

	loaded, err := LoadRleConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Rle.AgentName == nil || *loaded.Rle.AgentName != "support-agent" {
		t.Fatalf("expected normalized HostedAgent name, got %#v", loaded.Rle.AgentName)
	}
	if loaded.Rle.AgentVersion == nil || *loaded.Rle.AgentVersion != "12" {
		t.Fatalf("expected HostedAgent version, got %#v", loaded.Rle.AgentVersion)
	}
}

func TestWriteAndLoadRleConfigCanonicalizesVersionScopedDefaultsAndMetadata(t *testing.T) {
	dir := t.TempDir()
	schemaVersion := " 1.0.0 "
	modelName := " Qwen/Qwen3-32B "
	rendererName := " qwen3_disable_thinking "
	reasoningEffort := " HIGH "
	checkpointID := " "
	sampler := " default "
	config := RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: RleManifest{
			Name:    "code_rl",
			Version: "1.0.0",
			Type:    RleTypeGym,
			Subtype: RleSubtypeOpenEnv,
		},
		Defaults: &RleEnvironmentDefaults{
			Model: &RleModelDefaults{
				Name:         &modelName,
				RendererName: &rendererName,
			},
			Seed: intPointer(-17),
			Reinforcement: &RleReinforcementDefaults{
				MaxEpisodeSteps: intPointer(5),
				Hyperparameters: &RleReinforcementHyperparameters{
					NumberOfEpochs:         intPointer(3),
					BatchSize:              intPointer(8),
					LearningRateMultiplier: float64Pointer(0.25),
					EvalInterval:           intPointer(10),
					EvalSamples:            intPointer(20),
					ComputeMultiplier:      float64Pointer(1.5),
					ReasoningEffort:        &reasoningEffort,
				},
			},
			Grpo: &RleGrpoDefaults{
				GroupSize:      intPointer(8),
				GroupsPerBatch: intPointer(16),
				MaxSteps:       intPointer(100),
			},
			Loom: &RleLoomDefaults{
				CheckpointID: &checkpointID,
				LoraRank:     intPointer(32),
				Sampler:      &sampler,
			},
		},
		Metadata: map[string]string{
			" owner ": " rle-platform ",
		},
	}

	if err := WriteRleConfig(dir, config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, RleConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`schema_version = '1.0.0'`,
		`renderer_name = 'qwen3_disable_thinking'`,
		`n_epochs = 3`,
		`reasoning_effort = 'high'`,
		`groups_per_batch = 16`,
		`lora_rank = 32`,
		`owner = 'rle-platform'`,
	} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("expected config to contain %q, got:\n%s", expected, data)
		}
	}
	if strings.Contains(string(data), "checkpoint_id") {
		t.Fatalf("expected whitespace-only optional checkpoint ID to be omitted, got:\n%s", data)
	}

	loaded, err := LoadRleConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion == nil || *loaded.SchemaVersion != CurrentRleManifestSchemaVersion {
		t.Fatalf("expected normalized schema version, got %#v", loaded.SchemaVersion)
	}
	if loaded.Defaults == nil ||
		loaded.Defaults.Model == nil ||
		loaded.Defaults.Model.Name == nil || *loaded.Defaults.Model.Name != "Qwen/Qwen3-32B" ||
		loaded.Defaults.Reinforcement == nil ||
		loaded.Defaults.Reinforcement.Hyperparameters == nil ||
		loaded.Defaults.Reinforcement.Hyperparameters.ReasoningEffort == nil ||
		*loaded.Defaults.Reinforcement.Hyperparameters.ReasoningEffort != "high" ||
		loaded.Defaults.Loom == nil ||
		loaded.Defaults.Loom.CheckpointID != nil ||
		loaded.Metadata["owner"] != "rle-platform" {
		t.Fatalf("expected normalized defaults and metadata, got %#v", loaded)
	}
}

func TestNormalizeRleConfigValidatesVersionScopedMetadata(t *testing.T) {
	schemaVersion := CurrentRleManifestSchemaVersion
	base := RleManifest{
		Name:    "code_rl",
		Version: "1.0.0",
		Type:    RleTypeGym,
		Subtype: RleSubtypeOpenEnv,
	}
	tests := []struct {
		name     string
		config   RleConfig
		wantCode string
	}{
		{
			name: "schema version is required for defaults",
			config: RleConfig{
				Rle:      base,
				Defaults: &RleEnvironmentDefaults{},
			},
			wantCode: "rle_manifest_schema_version_required",
		},
		{
			name: "unsupported schema version",
			config: RleConfig{
				SchemaVersion: stringPointer("2.0.0"),
				Rle:           base,
			},
			wantCode: "rle_manifest_schema_version_invalid",
		},
		{
			name: "nonpositive default",
			config: RleConfig{
				SchemaVersion: &schemaVersion,
				Rle:           base,
				Defaults: &RleEnvironmentDefaults{
					Grpo: &RleGrpoDefaults{GroupSize: intPointer(0)},
				},
			},
			wantCode: "rle_manifest_default_invalid",
		},
		{
			name: "nonfinite default",
			config: RleConfig{
				SchemaVersion: &schemaVersion,
				Rle:           base,
				Defaults: &RleEnvironmentDefaults{
					Reinforcement: &RleReinforcementDefaults{
						Hyperparameters: &RleReinforcementHyperparameters{
							LearningRateMultiplier: float64Pointer(math.Inf(1)),
						},
					},
				},
			},
			wantCode: "rle_manifest_default_invalid",
		},
		{
			name: "unsupported reasoning effort",
			config: RleConfig{
				SchemaVersion: &schemaVersion,
				Rle:           base,
				Defaults: &RleEnvironmentDefaults{
					Reinforcement: &RleReinforcementDefaults{
						Hyperparameters: &RleReinforcementHyperparameters{
							ReasoningEffort: stringPointer("maximum"),
						},
					},
				},
			},
			wantCode: "rle_manifest_default_invalid",
		},
		{
			name: "empty metadata value",
			config: RleConfig{
				SchemaVersion: &schemaVersion,
				Rle:           base,
				Metadata:      map[string]string{"owner": " "},
			},
			wantCode: "rle_manifest_metadata_invalid",
		},
		{
			name: "duplicate normalized metadata key",
			config: RleConfig{
				SchemaVersion: &schemaVersion,
				Rle:           base,
				Metadata:      map[string]string{"owner": "one", " owner ": "two"},
			},
			wantCode: "rle_manifest_metadata_invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NormalizeRleConfig(test.config)
			var localErr *azdext.LocalError
			if !errors.As(err, &localErr) || localErr.Code != test.wantCode {
				t.Fatalf("expected LocalError code %q, got %v", test.wantCode, err)
			}
		})
	}
}

func TestNormalizeRleConfigAllowsLegacyManifestWithoutVersionScopedMetadata(t *testing.T) {
	config, err := NormalizeRleConfig(RleConfig{
		Rle: RleManifest{
			Name:    "code_rl",
			Version: "1.0.0",
			Type:    RleTypeGym,
			Subtype: RleSubtypeOpenEnv,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.SchemaVersion != nil || config.Defaults != nil || config.Metadata != nil {
		t.Fatalf("expected legacy manifest metadata to remain omitted, got %#v", config)
	}
}

func TestLoadRleConfigRejectsSchemaVersionInsideRleTable(t *testing.T) {
	dir := t.TempDir()
	content := `[rle]
schema_version = "1.0.0"
name = "code_rl"
version = "1.0.0"
type = "Gym"
subtype = "OpenEnv"
`
	if err := os.WriteFile(filepath.Join(dir, RleConfigFile), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadRleConfig(dir)
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_invalid" {
		t.Fatalf("expected an invalid-manifest error for schema_version inside [rle], got %v", err)
	}
}

func TestNormalizeRleConfigEnforcesControlPlaneTypeContract(t *testing.T) {
	tests := []struct {
		name     string
		manifest RleManifest
		wantCode string
		wantURL  string
	}{
		{
			name: "gym openenv",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
				Type:    "gym",
				Subtype: "openenv",
			},
		},
		{
			name: "hosted agent released version",
			manifest: RleManifest{
				Name:         "support_agent",
				Version:      "1.0.0",
				Type:         RleTypeHarness,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    stringPointer("support-agent"),
				AgentVersion: stringPointer("12"),
			},
		},
		{
			name: "hosted agent draft version",
			manifest: RleManifest{
				Name:         "support_agent",
				Version:      "1.0.0",
				Type:         RleTypeHarness,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    stringPointer("support-agent"),
				AgentVersion: stringPointer("draft-1767225600"),
			},
		},
		{
			name: "byoh",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
				Type:    RleTypeHarness,
				Subtype: RleSubtypeBYOH,
				BaseURL: stringPointer("https://Harness.Example.com/rle/"),
			},
			wantURL: "https://harness.example.com/rle/",
		},
		{
			name: "type required",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
				Subtype: RleSubtypeOpenEnv,
			},
			wantCode: "rle_manifest_type_invalid",
		},
		{
			name: "subtype required",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
				Type:    RleTypeGym,
			},
			wantCode: "rle_manifest_subtype_invalid",
		},
		{
			name: "gym rejects harness configuration",
			manifest: RleManifest{
				Name:      "code_rl",
				Version:   "1.0.0",
				Type:      RleTypeGym,
				Subtype:   RleSubtypeOpenEnv,
				AgentName: stringPointer("support-agent"),
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "gym rejects harness subtype",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
				Type:    RleTypeGym,
				Subtype: RleSubtypeHostedAgent,
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "hosted agent requires valid version",
			manifest: RleManifest{
				Name:         "support_agent",
				Version:      "1.0.0",
				Type:         RleTypeHarness,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    stringPointer("support-agent"),
				AgentVersion: stringPointer("v12"),
			},
			wantCode: "rle_agent_version_invalid",
		},
		{
			name: "hosted agent rejects base url",
			manifest: RleManifest{
				Name:         "support_agent",
				Version:      "1.0.0",
				Type:         RleTypeHarness,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    stringPointer("support-agent"),
				AgentVersion: stringPointer("12"),
				BaseURL:      stringPointer("https://agent.example.com"),
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "byoh rejects hosted fields",
			manifest: RleManifest{
				Name:      "customer_agent",
				Version:   "1.0.0",
				Type:      RleTypeHarness,
				Subtype:   RleSubtypeBYOH,
				AgentName: stringPointer("support-agent"),
				BaseURL:   stringPointer("https://harness.example.com"),
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "byoh requires https",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
				Type:    RleTypeHarness,
				Subtype: RleSubtypeBYOH,
				BaseURL: stringPointer("http://harness.example.com"),
			},
			wantCode: "rle_harness_base_url_invalid",
		},
		{
			name: "byoh rejects credentials",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
				Type:    RleTypeHarness,
				Subtype: RleSubtypeBYOH,
				BaseURL: stringPointer("https://user@harness.example.com"),
			},
			wantCode: "rle_harness_base_url_invalid",
		},
		{
			name: "byoh rejects query",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
				Type:    RleTypeHarness,
				Subtype: RleSubtypeBYOH,
				BaseURL: stringPointer("https://harness.example.com?token=secret"),
			},
			wantCode: "rle_harness_base_url_invalid",
		},
		{
			name: "legacy agent type is rejected",
			manifest: RleManifest{
				Name:    "support_agent",
				Version: "1.0.0",
				Type:    "Agent",
				Subtype: RleSubtypeHostedAgent,
			},
			wantCode: "rle_manifest_type_invalid",
		},
		{
			name: "legacy byoa subtype is rejected",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
				Type:    RleTypeHarness,
				Subtype: "BYOA",
			},
			wantCode: "rle_manifest_subtype_invalid",
		},
		{
			name: "rle version must match service semantics",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0",
				Type:    RleTypeGym,
				Subtype: RleSubtypeOpenEnv,
			},
			wantCode: "rle_manifest_version_invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := NormalizeRleConfig(RleConfig{Rle: test.manifest})
			if test.wantCode == "" {
				if err != nil {
					t.Fatal(err)
				}
				if config.Rle.Type == "" || config.Rle.Subtype == "" {
					t.Fatalf("expected canonical type/subtype, got %#v", config.Rle)
				}
				if test.wantURL != "" && (config.Rle.BaseURL == nil || *config.Rle.BaseURL != test.wantURL) {
					t.Fatalf("expected base URL %q, got %#v", test.wantURL, config.Rle.BaseURL)
				}
				return
			}

			var localErr *azdext.LocalError
			if !errors.As(err, &localErr) || localErr.Code != test.wantCode {
				t.Fatalf("expected LocalError code %q, got %v", test.wantCode, err)
			}
		})
	}
}

func TestLoadRleConfigRequiresManifest(t *testing.T) {
	_, err := LoadRleConfig(t.TempDir())
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_missing" {
		t.Fatalf("expected missing manifest error, got %v", err)
	}
}

func TestLoadRleConfigRejectsLegacyKindConfiguration(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RleConfigFile), []byte(`
[rle]
name = "support_agent"
version = "1.0.0"
kind = "hosted_agent"
`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadRleConfig(dir)
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_invalid" {
		t.Fatalf("expected legacy kind manifest to be rejected, got %v", err)
	}
}

func TestValidateInitialRleVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		wantErr string
	}{
		{name: "initial", version: "1.0.0"},
		{name: "wrong initial", version: "0.1.0", wantErr: "rle_manifest_initial_version_invalid"},
		{name: "later version", version: "1.0.1", wantErr: "rle_manifest_initial_version_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateInitialRleVersion(test.version)
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var localErr *azdext.LocalError
			if !errors.As(err, &localErr) || localErr.Code != test.wantErr {
				t.Fatalf("expected LocalError code %q, got %v", test.wantErr, err)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}

func intPointer(value int) *int {
	return &value
}

func float64Pointer(value float64) *float64 {
	return &value
}
