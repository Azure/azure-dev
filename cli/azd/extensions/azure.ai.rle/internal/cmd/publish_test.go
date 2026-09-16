// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestBuildEnvironmentCreateRequestMapsManifestConfiguration(t *testing.T) {
	agentName := "support-agent"
	agentVersion := "12"
	schemaVersion := project.CurrentRleManifestSchemaVersion
	modelName := "Qwen/Qwen3-32B"
	numberOfEpochs := 3
	config := project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: project.RleManifest{
			Name:         "support_rle",
			Version:      "1.0.1",
			Type:         project.RleTypeHarness,
			Subtype:      project.RleSubtypeHostedAgent,
			AgentName:    &agentName,
			AgentVersion: &agentVersion,
		},
		Defaults: &project.RleEnvironmentDefaults{
			Model: &project.RleModelDefaults{Name: &modelName},
			Reinforcement: &project.RleReinforcementDefaults{
				Hyperparameters: &project.RleReinforcementHyperparameters{
					NumberOfEpochs: &numberOfEpochs,
				},
			},
		},
	}

	request := buildEnvironmentCreateRequest(config, "example.azurecr.io/support_rle:1.0.1")
	if request.Name != "support_rle" ||
		request.Version != "1.0.1" ||
		request.Type != "Harness" ||
		request.Subtype != "HostedAgent" ||
		request.AgentName == nil || *request.AgentName != agentName ||
		request.AgentVersion == nil || *request.AgentVersion != agentVersion ||
		request.SchemaVersion == nil || *request.SchemaVersion != project.CurrentRleManifestSchemaVersion ||
		request.Defaults == nil || request.Defaults.Model == nil ||
		request.Defaults.Model.Name == nil || *request.Defaults.Model.Name != modelName ||
		request.BaseURL != nil {
		t.Fatalf("expected manifest data to map to create request, got %#v", request)
	}

	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]string{
		"name":          "support_rle",
		"version":       "1.0.1",
		"schemaVersion": project.CurrentRleManifestSchemaVersion,
		"type":          "Harness",
		"subtype":       "HostedAgent",
		"agentName":     agentName,
		"agentVersion":  agentVersion,
	} {
		if payload[key] != expected {
			t.Fatalf("expected %s=%q, got %#v", key, expected, payload[key])
		}
	}
	if _, exists := payload["baseUrl"]; exists {
		t.Fatalf("expected HostedAgent request to omit baseUrl, got %s", data)
	}
	if _, exists := payload["versionBump"]; exists {
		t.Fatalf("expected explicit version request to omit versionBump, got %s", data)
	}
	if _, exists := payload["metadata"]; exists {
		t.Fatalf("expected request to omit metadata, got %s", data)
	}
	defaults, ok := payload["defaults"].(map[string]any)
	if !ok {
		t.Fatalf("expected defaults payload, got %#v", payload["defaults"])
	}
	model, ok := defaults["model"].(map[string]any)
	if !ok || model["name"] != modelName {
		t.Fatalf("expected model default, got %#v", defaults["model"])
	}
	reinforcement, ok := defaults["reinforcement"].(map[string]any)
	if !ok {
		t.Fatalf("expected reinforcement defaults, got %#v", defaults["reinforcement"])
	}
	hyperparameters, ok := reinforcement["hyperparameters"].(map[string]any)
	if !ok || hyperparameters["n_epochs"] != float64(numberOfEpochs) {
		t.Fatalf("expected snake_case hyperparameter payload, got %#v", reinforcement["hyperparameters"])
	}
}

func TestBuildEnvironmentCreateRequestMapsByohHarnessConfiguration(t *testing.T) {
	baseURL := "https://harness.example.com/rollouts/"
	config := project.RleConfig{
		Rle: project.RleManifest{
			Name:    "customer_harness",
			Version: "1.0.1",
			Type:    project.RleTypeHarness,
			Subtype: project.RleSubtypeBYOH,
			BaseURL: &baseURL,
		},
	}

	request := buildEnvironmentCreateRequest(config, "example.azurecr.io/customer_harness:1.0.1")
	if request.Type != "Harness" ||
		request.Subtype != "BYOH" ||
		request.BaseURL == nil || *request.BaseURL != baseURL ||
		request.AgentName != nil ||
		request.AgentVersion != nil {
		t.Fatalf("expected BYOH manifest data to map to create request, got %#v", request)
	}
}

func TestResolvePublishImageUsesManifestVersion(t *testing.T) {
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "example.azurecr.io")

	image, err := resolvePublishImage(
		"code_rl",
		"1.2.0",
		"https://account.services.ai.azure.com/api/projects/project-name",
	)
	if err != nil {
		t.Fatal(err)
	}
	if image != "example.azurecr.io/project-name-code-rl:1.2.0" {
		t.Fatalf("unexpected versioned image name %q", image)
	}
}

func TestResolvePublishImageRequiresAcrRegistryForManifest(t *testing.T) {
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "")
	_, err := resolvePublishImage(
		"code_rl",
		"1.0.0",
		"https://account.services.ai.azure.com/api/projects/project-name",
	)
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_acr_registry_required" {
		t.Fatalf("expected ACR registry error, got %v", err)
	}
}

func TestVerifyPublishedEnvironmentRequiresManifestIdentity(t *testing.T) {
	config := project.RleConfig{Rle: project.RleManifest{
		Name:    "code_rl",
		Version: "1.0.0",
		Type:    project.RleTypeGym,
		Subtype: project.RleSubtypeOpenEnv,
	}}
	err := verifyPublishedEnvironment(config, &environmentResource{
		Name:    "code_rl",
		Version: "1.0.1",
		Type:    "Gym",
		Subtype: "OpenEnv",
	})
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_published_environment_mismatch" {
		t.Fatalf("expected published identity mismatch, got %v", err)
	}
}

func TestVerifyPublishedEnvironmentRequiresManifestDefaults(t *testing.T) {
	schemaVersion := project.CurrentRleManifestSchemaVersion
	modelName := "Qwen/Qwen3-32B"
	differentModelName := "Qwen/Qwen3-14B"
	config := project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: project.RleManifest{
			Name:    "code_rl",
			Version: "1.0.0",
			Type:    project.RleTypeGym,
			Subtype: project.RleSubtypeOpenEnv,
		},
		Defaults: &project.RleEnvironmentDefaults{
			Model: &project.RleModelDefaults{Name: &modelName},
		},
	}
	err := verifyPublishedEnvironment(config, &environmentResource{
		Name:          "code_rl",
		Version:       "1.0.0",
		Type:          "Gym",
		Subtype:       "OpenEnv",
		SchemaVersion: &schemaVersion,
		Defaults: &project.RleEnvironmentDefaults{
			Model: &project.RleModelDefaults{Name: &differentModelName},
		},
	})
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_published_environment_mismatch" {
		t.Fatalf("expected published defaults mismatch, got %v", err)
	}
}

func TestPublishRequiresManifestBeforeProjectConfiguration(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(foundryProjectEndpointEnvVar, "")

	command := newPublishCommand()
	err := command.Execute()
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_missing" {
		t.Fatalf("expected missing manifest error, got %v", err)
	}
}

func TestEnvironmentOutputUsesEnvironmentNameField(t *testing.T) {
	body, err := json.Marshal(environmentOutput{
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
		EnvironmentName:    "echo_env",
		Type:               "Gym",
		Subtype:            "OpenEnv",
	})
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["environmentName"] != "echo_env" {
		t.Fatalf("expected environmentName field, got %v", payload)
	}
	if _, exists := payload["name"]; exists {
		t.Fatalf("expected legacy name field to be omitted, got %v", payload)
	}
}

func TestResolvePublishTargetRequiresInitialManifestVersion(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(foundryProjectEndpointEnvVar, "https://account.services.ai.azure.com/api/projects/project")
	if err := project.WriteRleConfig(tempDir, project.RleConfig{
		Rle: project.RleManifest{
			Name:    "code_rl",
			Version: "0.1.0",
			Type:    project.RleTypeGym,
			Subtype: project.RleSubtypeOpenEnv,
		},
	}); err != nil {
		t.Fatal(err)
	}

	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet ||
			r.URL.Path != testFoundryProjectPath+environmentCollectionPath+"/code_rl" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		http.NotFound(w, r)
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	_, _, _, _, err := resolvePublishTarget(t.Context())
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_initial_version_invalid" {
		t.Fatalf("expected invalid initial version error, got %v", err)
	}

	if _, err := project.LoadRleConfig(tempDir); err != nil {
		t.Fatalf("expected manifest to remain unchanged after failed preflight: %v", err)
	}
}
