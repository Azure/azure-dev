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

func TestWriteAndLoadRleConfigCanonicalizesByohManifest(t *testing.T) {
	dir := t.TempDir()
	endpoint := " https://acct.services.ai.azure.com/api/projects/proj/agents/support-agent/endpoint/protocols/openai/responses "
	schemaVersion := CurrentRleManifestSchemaVersion
	config := RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: RleManifest{
			Name:    "support_agent",
			Version: "1.0.0",
		},
		Harness: RleHarnessManifest{
			Type:     HarnessTypeBYOH,
			Endpoint: &endpoint,
			Protocol: HarnessProtocolResponses,
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
		`type = 'byoh'`,
		`endpoint = 'https://acct.services.ai.azure.com/api/projects/proj/agents/support-agent/endpoint/protocols/openai/responses'`,
		`protocol = 'responses'`,
		`[harness]`,
	} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("expected config to contain %q, got:\n%s", expected, data)
		}
	}
	if schemaIndex, rleIndex := strings.Index(string(data), "schema_version"), strings.Index(string(data), "[rle]"); schemaIndex < 0 || rleIndex < 0 || schemaIndex > rleIndex {
		t.Fatalf("expected schema_version to precede the [rle] table, got:\n%s", data)
	}

	loaded, err := LoadRleConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Harness.Type != HarnessTypeBYOH || loaded.Harness.Protocol != HarnessProtocolResponses {
		t.Fatalf("unexpected harness %#v", loaded.Harness)
	}
	if loaded.Harness.Endpoint == nil || strings.Contains(*loaded.Harness.Endpoint, " ") {
		t.Fatalf("expected trimmed endpoint, got %#v", loaded.Harness.Endpoint)
	}
}

func TestWriteAndLoadRleConfigCanonicalizesVersionScopedDefaults(t *testing.T) {
	dir := t.TempDir()
	schemaVersion := " 1.0.0 "
	modelName := " Qwen/Qwen3-32B "
	rendererName := " qwen3_ext "
	modelResponseField := " answer_text "
	config := RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: RleManifest{
			Name:    "code_rl",
			Version: "1.0.0",
		},
		Harness: RleHarnessManifest{
			Type: HarnessTypeGym,
		},
		Model: &RleModelDefaults{
			Name:         &modelName,
			RendererName: &rendererName,
		},
		Train: &RleTrainDefaults{
			Grpo: &RleGrpoDefaults{
				GroupSize:      new(8),
				GroupsPerBatch: new(16),
				MaxSteps:       new(100),
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
		`renderer_name = 'qwen3_ext'`,
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
		Type: HarnessTypeGym,
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
				SchemaVersion: new("2.0.0"),
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
					Grpo: &RleGrpoDefaults{GroupSize: new(0)},
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
					Grpo: &RleGrpoDefaults{LearningRate: new(math.Inf(1))},
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
						ModelResponseField: new(strings.Repeat("a", maxRleModelResponseFieldLength+1)),
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
			Type: HarnessTypeGym,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.SchemaVersion != nil || config.Model != nil || config.Train != nil || config.Rollout != nil {
		t.Fatalf("expected legacy manifest defaults to remain omitted, got %#v", config)
	}
}

func TestLoadRleConfigRejectsRemovedDefaultsAndMetadata(t *testing.T) {
	base := `schema_version = "1.0.0"

[rle]
name = "code_rl"
version = "1.0.0"

[harness]
type = "gym"
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
type = "gym"
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
type = "byoh"
endpoint = "https://acct.services.ai.azure.com/api/projects/proj/agents/ci-agent/endpoint/protocols/openai/responses"
protocol = "responses"
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

func TestLoadRleConfigRejectsEnvironmentProtocol(t *testing.T) {
	for name, content := range map[string]string{
		"nested": `[rle]
name = "ci"
version = "2.0.0"
environmentProtocol = "mcp_environment"

[harness]
type = "byoh"
endpoint = "https://acct.services.ai.azure.com/api/projects/proj/agents/ci-agent/endpoint/protocols/openai/responses"
protocol = "responses"
`,
		"top-level": `environment_protocol = "mcp_environment"

[rle]
name = "ci"
version = "2.0.0"

[harness]
type = "byoh"
endpoint = "https://acct.services.ai.azure.com/api/projects/proj/agents/ci-agent/endpoint/protocols/openai/responses"
protocol = "responses"
`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, RleConfigFile), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}

			_, err := LoadRleConfig(dir)
			assertLocalErrorCode(t, err, "rle_manifest_invalid")
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

func TestNormalizeRleConfigEnforcesHarnessContract(t *testing.T) {
	manifest := RleManifest{Name: "support_agent", Version: "1.0.0"}
	foundryURL := "https://acct.services.ai.azure.com/api/projects/proj/agents/a/endpoint/protocols/openai/responses"
	tests := []struct {
		name     string
		harness  RleHarnessManifest
		wantType HarnessType
		wantURL  string
		wantCode string
	}{
		{name: "empty type defaults to gym", harness: RleHarnessManifest{}, wantType: HarnessTypeGym},
		{name: "gym", harness: RleHarnessManifest{Type: "gym"}, wantType: HarnessTypeGym},
		{
			name:     "byoh responses",
			harness:  RleHarnessManifest{Type: "byoh", Endpoint: new(foundryURL), Protocol: "responses"},
			wantType: HarnessTypeBYOH,
			wantURL:  foundryURL,
		},
		{
			name:     "byoh without protocol",
			harness:  RleHarnessManifest{Type: "byoh", Endpoint: new("https://Harness.Example.com/rle/")},
			wantType: HarnessTypeBYOH,
			wantURL:  "https://harness.example.com/rle/",
		},
		{
			name:     "byoh rle-invocation",
			harness:  RleHarnessManifest{Type: "byoh", Endpoint: new("https://harness.example.com"), Protocol: "rle-invocation"},
			wantType: HarnessTypeBYOH,
			wantURL:  "https://harness.example.com/",
		},
		{name: "capitalized type rejected", harness: RleHarnessManifest{Type: "Gym"}, wantCode: "rle_manifest_type_invalid"},
		{name: "legacy Harness type rejected", harness: RleHarnessManifest{Type: "Harness"}, wantCode: "rle_manifest_type_invalid"},
		{name: "gym rejects endpoint", harness: RleHarnessManifest{Type: "gym", Endpoint: new("https://h.example.com")}, wantCode: "rle_manifest_type_configuration_invalid"},
		{name: "gym rejects protocol", harness: RleHarnessManifest{Type: "gym", Protocol: "responses"}, wantCode: "rle_manifest_type_configuration_invalid"},
		{name: "byoh requires endpoint", harness: RleHarnessManifest{Type: "byoh"}, wantCode: "rle_harness_endpoint_required"},
		{name: "byoh rejects http", harness: RleHarnessManifest{Type: "byoh", Endpoint: new("http://h.example.com")}, wantCode: "rle_harness_endpoint_invalid"},
		{name: "byoh rejects credentials", harness: RleHarnessManifest{Type: "byoh", Endpoint: new("https://user@h.example.com")}, wantCode: "rle_harness_endpoint_invalid"},
		{name: "byoh rejects query", harness: RleHarnessManifest{Type: "byoh", Endpoint: new("https://h.example.com?token=x")}, wantCode: "rle_harness_endpoint_invalid"},
		{name: "byoh rejects fragment", harness: RleHarnessManifest{Type: "byoh", Endpoint: new("https://h.example.com/#x")}, wantCode: "rle_harness_endpoint_invalid"},
		{name: "byoh rejects unknown protocol", harness: RleHarnessManifest{Type: "byoh", Endpoint: new("https://h.example.com"), Protocol: "grpc"}, wantCode: "rle_harness_protocol_invalid"},
		{name: "package requires name", harness: RleHarnessManifest{Type: "package"}, wantCode: "rle_harness_name_required"},
		{name: "package requires harbor task", harness: RleHarnessManifest{Type: "package", Name: "opencode"}, wantCode: "rle_harness_package_requires_harbor"},
		{name: "gym rejects name", harness: RleHarnessManifest{Type: "gym", Name: "opencode"}, wantCode: "rle_harness_name_not_supported"},
		{name: "byoh rejects name", harness: RleHarnessManifest{Type: "byoh", Endpoint: new("https://h.example.com"), Name: "opencode"}, wantCode: "rle_harness_name_not_supported"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := NormalizeRleConfig(RleConfig{Rle: manifest, Harness: test.harness})
			if test.wantCode == "" {
				if err != nil {
					t.Fatal(err)
				}
				if config.Harness.Type != test.wantType {
					t.Fatalf("type = %q, want %q", config.Harness.Type, test.wantType)
				}
				if test.wantURL != "" && (config.Harness.Endpoint == nil || *config.Harness.Endpoint != test.wantURL) {
					t.Fatalf("endpoint = %v, want %q", config.Harness.Endpoint, test.wantURL)
				}
				return
			}
			assertLocalErrorCode(t, err, test.wantCode)
		})
	}
}

func TestLoadRleConfigRejectsLegacyHarnessKeys(t *testing.T) {
	for _, key := range []string{
		`subtype = "OpenEnv"`, `agent_name = "a"`, `agentName = "a"`, `agent_version = "1"`,
		`agentVersion = "1"`, `invocation_url = "https://h.example.com"`,
		`invocationUrl = "https://h.example.com"`, `baseUrl = "https://h.example.com"`,
	} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			content := "[rle]\nname = \"ci\"\nversion = \"1.0.0\"\n\n[harness]\ntype = \"byoh\"\n" + key + "\n"
			if err := os.WriteFile(filepath.Join(dir, RleConfigFile), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadRleConfig(dir)
			assertLocalErrorCode(t, err, "rle_manifest_legacy_harness_key")
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

func TestLoadRleConfigValidatesTaskTable(t *testing.T) {
	base := "[rle]\nname = \"code_rl\"\nversion = \"1.0.0\"\n\n[harness]\ntype = \"gym\"\n"
	tests := []struct {
		name     string
		task     string
		wantCode string
	}{
		{name: "json default", task: "\n[task]\nformat = \"json\"\n"},
		{name: "omitted format", task: "\n[task]\n"},
		{name: "harbor requires package harness", task: "\n[task]\nformat = \"harbor\"\n", wantCode: "rle_task_harbor_requires_package"},
		{name: "unknown format", task: "\n[task]\nformat = \"yaml\"\n", wantCode: "rle_task_format_invalid"},
		{name: "uppercase rejected", task: "\n[task]\nformat = \"Harbor\"\n", wantCode: "rle_task_format_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, RleConfigFile), []byte(base+test.task), 0o600); err != nil {
				t.Fatal(err)
			}
			config, err := LoadRleConfig(dir)
			if test.wantCode != "" {
				assertLocalErrorCode(t, err, test.wantCode)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.Task != nil {
				t.Fatalf("json task should normalize to omitted, got %#v", config.Task)
			}
		})
	}
}
