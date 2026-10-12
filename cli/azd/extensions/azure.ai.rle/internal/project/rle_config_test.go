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

// ptr returns a pointer to a copy of value, for building test fixtures inline.
func ptr[T any](value T) *T {
	return &value
}

func TestWriteAndLoadRleConfigCanonicalizesHostedAgentManifest(t *testing.T) {
	dir := t.TempDir()
	agentName := " support-agent "
	agentVersion := "12"
	schemaVersion := CurrentRleManifestSchemaVersion
	config := RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: RleManifest{
			Name:    "support_agent",
			Version: "1.0.0",
		},
		Harness: RleHarnessManifest{
			Type:         RleTypeHarness,
			Subtype:      RleSubtypeHostedAgent,
			AgentName:    &agentName,
			AgentVersion: &agentVersion,
		},
	}

	if err := WriteRleConfig(dir, config); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- dir is created by t.TempDir.
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
		`agent_name = 'support-agent'`,
		`agent_version = '12'`,
		`[harness]`,
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
	if loaded.Harness.AgentName == nil || *loaded.Harness.AgentName != "support-agent" {
		t.Fatalf("expected normalized HostedAgent name, got %#v", loaded.Harness.AgentName)
	}
	if loaded.Harness.AgentVersion == nil || *loaded.Harness.AgentVersion != "12" {
		t.Fatalf("expected HostedAgent version, got %#v", loaded.Harness.AgentVersion)
	}
}

func TestWriteAndLoadRleConfigCanonicalizesVersionScopedDefaults(t *testing.T) {
	dir := t.TempDir()
	schemaVersion := " 1.0.0 "
	modelName := " Qwen/Qwen3-32B "
	rendererName := " qwen3_disable_thinking "
	modelResponseField := " answer_text "
	config := RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: RleManifest{
			Name:    "code_rl",
			Version: "1.0.0",
		},
		Harness: RleHarnessManifest{
			Type:    RleTypeGym,
			Subtype: RleSubtypeOpenEnv,
		},
		Model: &RleModelDefaults{
			Name:         &modelName,
			RendererName: &rendererName,
		},
		Train: &RleTrainDefaults{
			Grpo: &RleGrpoDefaults{
				GroupSize:      ptr(8),
				GroupsPerBatch: ptr(16),
				MaxSteps:       ptr(100),
			},
		},
		Rollout: &RleRolloutDefaults{
			GymOpenEnv: &RleGymOpenEnvDefaults{
				ModelResponseField: &modelResponseField,
			},
		},
	}

	if err := WriteRleConfig(dir, config); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- dir is created by t.TempDir.
	data, err := os.ReadFile(filepath.Join(dir, RleConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`schema_version = '1.0.0'`,
		`renderer_name = 'qwen3_disable_thinking'`,
		`groups_per_batch = 16`,
		`group_size = 8`,
		`max_steps = 100`,
		`model_response_field = 'answer_text'`,
		`[train.grpo]`,
		`[rollout.gym_openenv]`,
	} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("expected config to contain %q, got:\n%s", expected, data)
		}
	}
	for _, unexpected := range []string{
		"seed", "loom", "metadata",
		"n_epochs", "reasoning_effort", "batch_size", "learning_rate_multiplier",
		"eval_interval", "eval_samples", "compute_multiplier", "max_episode_steps",
		"max_completion_tokens",
	} {
		if strings.Contains(string(data), unexpected) {
			t.Fatalf("expected config to omit removed field %q, got:\n%s", unexpected, data)
		}
	}

	loaded, err := LoadRleConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion == nil || *loaded.SchemaVersion != CurrentRleManifestSchemaVersion {
		t.Fatalf("expected normalized schema version, got %#v", loaded.SchemaVersion)
	}
	if loaded.Model == nil ||
		loaded.Model.Name == nil || *loaded.Model.Name != "Qwen/Qwen3-32B" ||
		loaded.Train == nil ||
		loaded.Train.Grpo == nil ||
		loaded.Train.Grpo.GroupsPerBatch == nil || *loaded.Train.Grpo.GroupsPerBatch != 16 {
		t.Fatalf("expected normalized defaults, got %#v", loaded)
	}
	if loaded.Rollout == nil ||
		loaded.Rollout.GymOpenEnv == nil ||
		loaded.Rollout.GymOpenEnv.ModelResponseField == nil ||
		*loaded.Rollout.GymOpenEnv.ModelResponseField != "answer_text" {
		t.Fatalf("expected normalized Gym/OpenEnv rollout defaults, got %#v", loaded.Rollout)
	}
}

func TestNormalizeRleConfigValidatesVersionScopedDefaults(t *testing.T) {
	schemaVersion := CurrentRleManifestSchemaVersion
	base := RleManifest{
		Name:    "code_rl",
		Version: "1.0.0",
	}
	baseHarness := RleHarnessManifest{
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
				Rle:     base,
				Harness: baseHarness,
				Model:   &RleModelDefaults{},
			},
			wantCode: "rle_manifest_schema_version_required",
		},
		{
			name: "unsupported schema version",
			config: RleConfig{
				SchemaVersion: ptr("2.0.0"),
				Rle:           base,
				Harness:       baseHarness,
			},
			wantCode: "rle_manifest_schema_version_invalid",
		},
		{
			name: "nonpositive default",
			config: RleConfig{
				SchemaVersion: &schemaVersion,
				Rle:           base,
				Harness:       baseHarness,
				Train: &RleTrainDefaults{
					Grpo: &RleGrpoDefaults{GroupSize: ptr(0)},
				},
			},
			wantCode: "rle_manifest_default_invalid",
		},
		{
			name: "nonfinite default",
			config: RleConfig{
				SchemaVersion: &schemaVersion,
				Rle:           base,
				Harness:       baseHarness,
				Train: &RleTrainDefaults{
					Grpo: &RleGrpoDefaults{LearningRate: ptr(math.Inf(1))},
				},
			},
			wantCode: "rle_manifest_default_invalid",
		},
		{
			name: "rate out of bounds",
			config: RleConfig{
				SchemaVersion: &schemaVersion,
				Rle:           base,
				Harness:       baseHarness,
				Train: &RleTrainDefaults{
					Grpo: &RleGrpoDefaults{RolloutFailureRateAbort: ptr(1.5)},
				},
			},
			wantCode: "rle_manifest_default_invalid",
		},
		{
			name: "overlong model response field",
			config: RleConfig{
				SchemaVersion: &schemaVersion,
				Rle:           base,
				Harness:       baseHarness,
				Rollout: &RleRolloutDefaults{
					GymOpenEnv: &RleGymOpenEnvDefaults{
						ModelResponseField: ptr(strings.Repeat("a", maxRleModelResponseFieldLength+1)),
					},
				},
			},
			wantCode: "rle_manifest_default_invalid",
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

func TestNormalizeRleConfigAllowsLegacyManifestWithoutVersionScopedDefaults(t *testing.T) {
	config, err := NormalizeRleConfig(RleConfig{
		Rle: RleManifest{
			Name:    "code_rl",
			Version: "1.0.0",
		},
		Harness: RleHarnessManifest{
			Type:    RleTypeGym,
			Subtype: RleSubtypeOpenEnv,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.SchemaVersion != nil || config.Model != nil || config.Train != nil || config.Rollout != nil {
		t.Fatalf("expected legacy manifest defaults to remain omitted, got %#v", config)
	}
}

func TestNormalizeRleConfigDefaultsEnvironmentProtocolForCurrentSchemaVersion(t *testing.T) {
	t.Run("current schema version defaults to mcp_environment", func(t *testing.T) {
		schemaVersion := CurrentRleManifestSchemaVersion
		config, err := NormalizeRleConfig(RleConfig{
			SchemaVersion: &schemaVersion,
			Rle: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
			},
			Harness: RleHarnessManifest{
				Type:    RleTypeGym,
				Subtype: RleSubtypeOpenEnv,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if config.Rle.EnvironmentProtocol == nil ||
			*config.Rle.EnvironmentProtocol != RleEnvironmentProtocolMcpEnvironment {
			t.Fatalf(
				"expected an absent protocol with schema_version %q to default to %q, got %v",
				CurrentRleManifestSchemaVersion, RleEnvironmentProtocolMcpEnvironment, config.Rle.EnvironmentProtocol,
			)
		}
	})

	t.Run("absent schema version stays legacy", func(t *testing.T) {
		config, err := NormalizeRleConfig(RleConfig{
			Rle: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
			},
			Harness: RleHarnessManifest{
				Type:    RleTypeGym,
				Subtype: RleSubtypeOpenEnv,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if config.Rle.EnvironmentProtocol != nil {
			t.Fatalf(
				"expected a manifest with no schema_version to stay legacy, got %v",
				config.Rle.EnvironmentProtocol,
			)
		}
	})
}

func TestLoadRleConfigRejectsRemovedDefaultsAndMetadata(t *testing.T) {
	base := `schema_version = "1.0.0"

[rle]
name = "code_rl"
version = "1.0.0"

[harness]
type = "Gym"
subtype = "OpenEnv"
`
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "seed",
			content: base + "\n[train]\nseed = 17\n",
		},
		{
			name:    "loom",
			content: base + "\n[rollout.loom]\nlora_rank = 32\n",
		},
		{
			name:    "metadata",
			content: base + "\n[metadata]\nowner = \"rle-platform\"\n",
		},
		{
			name:    "reinforcement",
			content: base + "\n[train.reinforcement]\nmax_episode_steps = 4\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, RleConfigFile), []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}

			_, err := LoadRleConfig(dir)
			var localErr *azdext.LocalError
			if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_invalid" {
				t.Fatalf("expected an invalid-manifest error for removed field, got %v", err)
			}
		})
	}
}

func TestLoadRleConfigRejectsSchemaVersionInsideRleTable(t *testing.T) {
	dir := t.TempDir()
	content := `[rle]
schema_version = "1.0.0"
name = "code_rl"
version = "1.0.0"

[harness]
type = "Gym"
subtype = "OpenEnv"
`
	if err := os.WriteFile(filepath.Join(dir, RleConfigFile), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadRleConfig(dir)
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_invalid" {
		t.Fatalf("expected an invalid-manifest error for schema_version inside [rle], got %v", err)
	}
}

func TestLoadRleConfigAcceptsAPinnedImageTag(t *testing.T) {
	dir := t.TempDir()
	content := `[rle]
name = "competitive_intelligence_agent"
version = "1.0.16"
imageTag = "1.0.3"

[harness]
type = "Harness"
subtype = "HostedAgent"
agent_name = "ci-agent"
agent_version = "15"
`
	if err := os.WriteFile(filepath.Join(dir, RleConfigFile), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadRleConfig(dir)
	if err != nil {
		t.Fatalf("a manifest that pins an image tag should load, got %v", err)
	}
	if config.Rle.ImageTag == nil || *config.Rle.ImageTag != "1.0.3" {
		t.Fatalf("imageTag = %v, want 1.0.3", config.Rle.ImageTag)
	}
	if config.Rle.Version != "1.0.16" {
		t.Fatalf("version = %q, want 1.0.16: the image tag must not overwrite it", config.Rle.Version)
	}
}

func TestLoadRleConfigCarriesTheMcpEnvironmentProtocol(t *testing.T) {
	dir := t.TempDir()
	content := `[rle]
name = "competitive_intelligence_agent"
version = "2.0.0"
environmentProtocol = "  MCP_Environment  "

[harness]
type = "Harness"
subtype = "HostedAgent"
agent_name = "ci-agent"
agent_version = "15"
`
	if err := os.WriteFile(filepath.Join(dir, RleConfigFile), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadRleConfig(dir)
	if err != nil {
		t.Fatalf("a manifest that names the MCP protocol should load, got %v", err)
	}
	if config.Rle.EnvironmentProtocol == nil {
		t.Fatal("environmentProtocol was dropped: publish would silently register a legacy environment")
	}
	if *config.Rle.EnvironmentProtocol != RleEnvironmentProtocolMcpEnvironment {
		t.Fatalf("environmentProtocol = %q, want %q", *config.Rle.EnvironmentProtocol, RleEnvironmentProtocolMcpEnvironment)
	}

	roundTrip := t.TempDir()
	if err := WriteRleConfig(roundTrip, config); err != nil {
		t.Fatalf("write: %v", err)
	}
	reloaded, err := LoadRleConfig(roundTrip)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Rle.EnvironmentProtocol == nil ||
		*reloaded.Rle.EnvironmentProtocol != RleEnvironmentProtocolMcpEnvironment {
		t.Fatalf("protocol did not survive a canonical write, got %v", reloaded.Rle.EnvironmentProtocol)
	}
}

func TestNormalizeRleConfigRestrictsTheEnvironmentProtocol(t *testing.T) {
	hostedAgent := func(protocol *RleEnvironmentProtocol) RleConfig {
		return RleConfig{
			Rle: RleManifest{
				Name:                "competitive_intelligence_agent",
				Version:             "2.0.0",
				EnvironmentProtocol: protocol,
			},
			Harness: RleHarnessManifest{
				Type:         RleTypeHarness,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    ptr("ci-agent"),
				AgentVersion: ptr("15"),
			},
		}
	}

	t.Run("omitted means legacy", func(t *testing.T) {
		config, err := NormalizeRleConfig(hostedAgent(nil))
		if err != nil {
			t.Fatalf("omitting the protocol must stay valid, got %v", err)
		}
		if config.Rle.EnvironmentProtocol != nil {
			t.Fatal("an absent protocol must not be defaulted: the service reads absence as legacy")
		}
	})

	t.Run("empty is absent, not invalid", func(t *testing.T) {
		config, err := NormalizeRleConfig(hostedAgent(ptr(RleEnvironmentProtocol(""))))
		if err != nil {
			t.Fatalf("an empty protocol should normalize away, got %v", err)
		}
		if config.Rle.EnvironmentProtocol != nil {
			t.Fatalf("empty protocol survived as %v", config.Rle.EnvironmentProtocol)
		}
	})

	t.Run("byoh may carry it", func(t *testing.T) {
		_, err := NormalizeRleConfig(RleConfig{
			Rle: RleManifest{
				Name:                "customer_agent",
				Version:             "2.0.0",
				EnvironmentProtocol: ptr(RleEnvironmentProtocolMcpEnvironment),
			},
			Harness: RleHarnessManifest{
				Type:          RleTypeHarness,
				Subtype:       RleSubtypeBYOH,
				InvocationURL: ptr("https://harness.example.com/rle/"),
			},
		})
		if err != nil {
			t.Fatalf("BYOH is one of the two subtypes the service allows, got %v", err)
		}
	})

	t.Run("no other protocol is nameable", func(t *testing.T) {
		_, err := NormalizeRleConfig(hostedAgent(ptr(RleEnvironmentProtocol("legacy"))))
		assertLocalErrorCode(t, err, "rle_manifest_environment_protocol_invalid")
	})

	t.Run("gym openenv may carry it", func(t *testing.T) {
		config, err := NormalizeRleConfig(RleConfig{
			Rle: RleManifest{
				Name:                "code_rl",
				Version:             "2.0.0",
				EnvironmentProtocol: ptr(RleEnvironmentProtocolMcpEnvironment),
			},
			Harness: RleHarnessManifest{
				Type:    RleTypeGym,
				Subtype: RleSubtypeOpenEnv,
			},
		})
		if err != nil {
			t.Fatalf("Gym/OpenEnv must support MCP, got %v", err)
		}
		if config.Rle.EnvironmentProtocol == nil ||
			*config.Rle.EnvironmentProtocol != RleEnvironmentProtocolMcpEnvironment {
			t.Fatal("Gym/OpenEnv protocol was dropped")
		}
	})
}

func TestLoadRleConfigTopLevelEnvironmentProtocol(t *testing.T) {
	for _, sample := range []string{"math_rl", "code_rl"} {
		t.Run(sample, func(t *testing.T) {
			dir := t.TempDir()
			content := `schema_version = "1.0.0"
environment_protocol = "  MCP_Environment  "

[rle]
name = "` + sample + `"
version = "1.0.0"

[harness]
type = "Gym"
subtype = "OpenEnv"

[rollout.gym_openenv]
model_response_field = "answer"
`
			if err := os.WriteFile(filepath.Join(dir, RleConfigFile), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			config, err := LoadRleConfig(dir)
			if err != nil {
				t.Fatal(err)
			}
			if config.Rle.EnvironmentProtocol == nil ||
				*config.Rle.EnvironmentProtocol != RleEnvironmentProtocolMcpEnvironment {
				t.Fatal("top-level MCP protocol was dropped")
			}
			if config.EnvironmentProtocol != nil {
				t.Fatal("normalized protocol must have only one canonical location")
			}
			if err := WriteRleConfig(dir, config); err != nil {
				t.Fatal(err)
			}
			reloaded, err := LoadRleConfig(dir)
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Rle.EnvironmentProtocol == nil ||
				*reloaded.Rle.EnvironmentProtocol != RleEnvironmentProtocolMcpEnvironment {
				t.Fatal("top-level protocol did not survive a canonical write")
			}
		})
	}
}

func TestNormalizeRleConfigValidatesBothProtocolLocations(t *testing.T) {
	for _, test := range []struct {
		name   string
		top    RleEnvironmentProtocol
		nested RleEnvironmentProtocol
		valid  bool
	}{
		{"matching aliases", "mcp_environment", " MCP_Environment ", true},
		{"invalid top-level", "legacy", "mcp_environment", false},
		{"invalid nested", "mcp_environment", "legacy", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := NormalizeRleConfig(RleConfig{
				EnvironmentProtocol: ptr(test.top),
				Rle: RleManifest{
					Name: "code_rl", Version: "1.0.0",
					EnvironmentProtocol: ptr(test.nested),
				},
				Harness: RleHarnessManifest{
					Type:    RleTypeGym,
					Subtype: RleSubtypeOpenEnv,
				},
			})
			if !test.valid {
				assertLocalErrorCode(t, err, "rle_manifest_environment_protocol_invalid")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.Rle.EnvironmentProtocol == nil ||
				*config.Rle.EnvironmentProtocol != RleEnvironmentProtocolMcpEnvironment {
				t.Fatal("matching MCP aliases were dropped")
			}
		})
	}
}

func assertLocalErrorCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) {
		t.Fatalf("expected a local error with code %s, got %v", wantCode, err)
	}
	if localErr.Code != wantCode {
		t.Fatalf("error code = %q, want %q", localErr.Code, wantCode)
	}
}

func TestNormalizeRleConfigEnforcesControlPlaneTypeContract(t *testing.T) {
	tests := []struct {
		name     string
		manifest RleManifest
		harness  RleHarnessManifest
		wantCode string
		wantURL  string
	}{
		{
			name: "gym openenv",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:    "gym",
				Subtype: "openenv",
			},
		},
		{
			name: "hosted agent released version",
			manifest: RleManifest{
				Name:    "support_agent",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:         RleTypeHarness,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    ptr("support-agent"),
				AgentVersion: ptr("12"),
			},
		},
		{
			name: "hosted agent draft version",
			manifest: RleManifest{
				Name:    "support_agent",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:         RleTypeHarness,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    ptr("support-agent"),
				AgentVersion: ptr("draft-1767225600"),
			},
		},
		{
			name: "byoh",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:          RleTypeHarness,
				Subtype:       RleSubtypeBYOH,
				InvocationURL: ptr("https://Harness.Example.com/rle/"),
			},
			wantURL: "https://harness.example.com/rle/",
		},
		{
			name: "type required",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Subtype: RleSubtypeOpenEnv,
			},
			wantCode: "rle_manifest_type_invalid",
		},
		{
			name: "subtype required",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type: RleTypeGym,
			},
			wantCode: "rle_manifest_subtype_invalid",
		},
		{
			name: "gym rejects harness configuration",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:      RleTypeGym,
				Subtype:   RleSubtypeOpenEnv,
				AgentName: ptr("support-agent"),
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "gym rejects harness subtype",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:    RleTypeGym,
				Subtype: RleSubtypeHostedAgent,
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "hosted agent requires valid version",
			manifest: RleManifest{
				Name:    "support_agent",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:         RleTypeHarness,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    ptr("support-agent"),
				AgentVersion: ptr("v12"),
			},
			wantCode: "rle_agent_version_invalid",
		},
		{
			name: "hosted agent rejects base url",
			manifest: RleManifest{
				Name:    "support_agent",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:          RleTypeHarness,
				Subtype:       RleSubtypeHostedAgent,
				AgentName:     ptr("support-agent"),
				AgentVersion:  ptr("12"),
				InvocationURL: ptr("https://agent.example.com"),
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "byoh rejects hosted fields",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:          RleTypeHarness,
				Subtype:       RleSubtypeBYOH,
				AgentName:     ptr("support-agent"),
				InvocationURL: ptr("https://harness.example.com"),
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "byoh requires https",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:          RleTypeHarness,
				Subtype:       RleSubtypeBYOH,
				InvocationURL: ptr("http://harness.example.com"),
			},
			wantCode: "rle_harness_invocation_url_invalid",
		},
		{
			name: "byoh rejects credentials",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:          RleTypeHarness,
				Subtype:       RleSubtypeBYOH,
				InvocationURL: ptr("https://user@harness.example.com"),
			},
			wantCode: "rle_harness_invocation_url_invalid",
		},
		{
			name: "byoh rejects query",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
				Type:          RleTypeHarness,
				Subtype:       RleSubtypeBYOH,
				InvocationURL: ptr("https://harness.example.com?token=secret"),
			},
			wantCode: "rle_harness_invocation_url_invalid",
		},
		{
			name: "legacy agent type is rejected",
			manifest: RleManifest{
				Name:    "support_agent",
				Version: "1.0.0",
			},
			harness: RleHarnessManifest{
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
			},
			harness: RleHarnessManifest{
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
			},
			harness: RleHarnessManifest{
				Type:    RleTypeGym,
				Subtype: RleSubtypeOpenEnv,
			},
			wantCode: "rle_manifest_version_invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := NormalizeRleConfig(RleConfig{Rle: test.manifest, Harness: test.harness})
			if test.wantCode == "" {
				if err != nil {
					t.Fatal(err)
				}
				if config.Harness.Type == "" || config.Harness.Subtype == "" {
					t.Fatalf("expected canonical type/subtype, got %#v", config.Harness)
				}
				if test.wantURL != "" {
					if config.Harness.InvocationURL == nil || *config.Harness.InvocationURL != test.wantURL {
						t.Fatalf("expected base URL %q, got %#v", test.wantURL, config.Harness)
					}
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
`), 0600); err != nil {
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
