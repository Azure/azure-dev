// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestMain(m *testing.M) {
	// The publish regression test uses this binary instead of a real container runtime.
	if os.Getenv("RLE_TEST_PUBLISH_CONTAINER_RUNTIME") == "1" {
		if len(os.Args) > 1 && (os.Args[1] == "build" || os.Args[1] == "push") {
			os.Exit(0)
		}
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestPublishDisplaysReleaseIdentityAndPreservesJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("AZURE_CONTAINER_REGISTRY_ENDPOINT", "example.azurecr.io")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AZD_CONTAINER_RUNTIME", executable)
	t.Setenv("RLE_TEST_PUBLISH_CONTAINER_RUNTIME", "1")
	config := project.RleConfig{
		SchemaVersion: ptrTo(project.CurrentRleManifestSchemaVersion),
		Rle: project.RleManifest{
			Name: "math_rl", Version: "1.0.0",
		},
		Harness: project.RleHarnessManifest{
			Type: project.RleTypeGym, Subtype: project.RleSubtypeOpenEnv,
		},
		Model: &project.RleModelDefaults{Name: new("test-model")},
	}
	if err := project.WriteRleConfig(dir, config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	published := environmentResource{
		Id: "opaque-environment-id", Name: config.Rle.Name, Version: config.Rle.Version,
		Harness:      rleHarness{Type: "Gym", Subtype: "OpenEnv"},
		AcrImagePath: "example.azurecr.io/project-1-math-rl:1.0.0",
		Model:        config.Model, CreatedAt: "2026-07-30T04:00:00Z", UpdatedAt: "2026-07-30T05:00:00Z",
		SchemaVersion: config.SchemaVersion,
	}
	requestCount := 0
	controlPlane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		switch {
		case r.Method == http.MethodGet && r.URL.Path == testFoundryProjectPath+environmentCollectionPath+"/math_rl":
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == testFoundryProjectPath+environmentCollectionPath:
			var request v1EnvironmentRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			if request.Name != published.Name || request.Version != published.Version ||
				request.Harness.Type != published.Harness.Type || request.Harness.Subtype != published.Harness.Subtype ||
				request.AcrImagePath != published.AcrImagePath {
				t.Errorf("unexpected publish request: %#v", request)
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(published); err != nil {
				t.Error(err)
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer controlPlane.Close()
	stubRleClientEndpoint(t, controlPlane.URL)

	command := newPublishCommand()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	if err := command.Execute(); err != nil {
		t.Fatalf("publish failed: %v; stderr: %s", err, stderr.String())
	}
	if requestCount != 2 {
		t.Fatalf("expected preflight and publish requests, got %d", requestCount)
	}
	const success = "\nPublished environment 'math_rl' version 1.0.0\n"
	_, details, found := strings.Cut(stdout.String(), success)
	if !found {
		t.Fatalf("expected exact success line %q, got %s", success, stdout.String())
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(details), &payload); err != nil {
		t.Fatalf("expected JSON after success line, got %s: %v", details, err)
	}
	for field, want := range map[string]string{
		"environmentId": published.Id, "environmentName": published.Name, "environmentVersion": published.Version,
		"type": published.Harness.Type, "subtype": published.Harness.Subtype, "acrImage": published.AcrImagePath,
		"foundryProjectEndpoint": "https://account.services.ai.azure.com/api/projects/project-1",
		"createdAt":              published.CreatedAt, "updatedAt": published.UpdatedAt,
		"schema_version": *published.SchemaVersion,
	} {
		if payload[field] != want {
			t.Errorf("%s = %v, want %q", field, payload[field], want)
		}
	}
	if model, ok := payload["model"].(map[string]any); !ok || model["name"] == nil {
		t.Fatalf("expected model defaults to remain in JSON, got %#v", payload["model"])
	}
	t.Logf("Captured publish output:\n%s", stdout.String())
}

func TestBuildEnvironmentCreateRequestMapsManifestConfiguration(t *testing.T) {
	agentName := "support-agent"
	agentVersion := "12"
	schemaVersion := project.CurrentRleManifestSchemaVersion
	modelName := "Qwen/Qwen3-32B"
	numberOfEpochs := 3
	config := project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle: project.RleManifest{
			Name:    "support_rle",
			Version: "1.0.1",
		},
		Harness: project.RleHarnessManifest{
			Type:         project.RleTypeHarness,
			Subtype:      project.RleSubtypeHostedAgent,
			AgentName:    &agentName,
			AgentVersion: &agentVersion,
		},
		Model: &project.RleModelDefaults{Name: &modelName},
		Train: &project.RleTrainDefaults{
			Grpo: &project.RleGrpoDefaults{
				NumEpochs: &numberOfEpochs,
			},
		},
	}

	request := buildEnvironmentCreateRequest(config, "example.azurecr.io/support_rle:1.0.1")
	if request.Name != "support_rle" ||
		request.Version != "1.0.1" ||
		request.Harness.Type != "Harness" ||
		request.Harness.Subtype != "HostedAgent" ||
		request.Harness.AgentName == nil || *request.Harness.AgentName != agentName ||
		request.Harness.AgentVersion == nil || *request.Harness.AgentVersion != agentVersion ||
		request.SchemaVersion == nil || *request.SchemaVersion != project.CurrentRleManifestSchemaVersion ||
		request.Model == nil ||
		request.Model.Name == nil || *request.Model.Name != modelName ||
		request.Harness.InvocationURL != nil {
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
	harness, ok := payload["harness"].(map[string]any)
	if !ok {
		t.Fatalf("expected harness object, got %#v", payload["harness"])
	}
	for key, expected := range map[string]string{
		"type":          "Harness",
		"subtype":       "HostedAgent",
		"agent_name":    agentName,
		"agent_version": agentVersion,
	} {
		if harness[key] != expected {
			t.Fatalf("expected harness.%s=%q, got %#v", key, expected, harness[key])
		}
	}
	for key, expected := range map[string]string{
		"name":           "support_rle",
		"version":        "1.0.1",
		"schema_version": project.CurrentRleManifestSchemaVersion,
	} {
		if payload[key] != expected {
			t.Fatalf("expected %s=%q, got %#v", key, expected, payload[key])
		}
	}
	if _, exists := harness["invocation_url"]; exists {
		t.Fatalf("expected HostedAgent request to omit invocation_url, got %s", data)
	}
	if _, exists := payload["versionBump"]; exists {
		t.Fatalf("expected explicit version request to omit versionBump, got %s", data)
	}
	if _, exists := payload["metadata"]; exists {
		t.Fatalf("expected request to omit metadata, got %s", data)
	}
	model, ok := payload["model"].(map[string]any)
	if !ok || model["name"] != modelName {
		t.Fatalf("expected model default, got %#v", payload["model"])
	}
	train, ok := payload["train"].(map[string]any)
	if !ok {
		t.Fatalf("expected train defaults, got %#v", payload["train"])
	}
	grpo, ok := train["grpo"].(map[string]any)
	if !ok || grpo["num_epochs"] != float64(numberOfEpochs) {
		t.Fatalf("expected snake_case grpo payload, got %#v", train["grpo"])
	}
}

func TestBuildEnvironmentCreateRequestMapsByohHarnessConfiguration(t *testing.T) {
	invocationURL := "https://harness.example.com/rollouts/"
	config := project.RleConfig{Rle: project.RleManifest{
		Name:    "customer_harness",
		Version: "1.0.1",
	}, Harness: project.RleHarnessManifest{
		Type:          project.RleTypeHarness,
		Subtype:       project.RleSubtypeBYOH,
		InvocationURL: &invocationURL,
	}}

	request := buildEnvironmentCreateRequest(config, "example.azurecr.io/customer_harness:1.0.1")
	if request.Harness.Type != "Harness" ||
		request.Harness.Subtype != "BYOH" ||
		request.Harness.InvocationURL == nil || *request.Harness.InvocationURL != invocationURL ||
		request.Harness.AgentName != nil ||
		request.Harness.AgentVersion != nil {
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
	}, Harness: project.RleHarnessManifest{
		Type:    project.RleTypeGym,
		Subtype: project.RleSubtypeOpenEnv,
	}}
	err := verifyPublishedEnvironment(config, &environmentResource{
		Name:    "code_rl",
		Version: "1.0.1",
		Harness: rleHarness{Type: "Gym", Subtype: "OpenEnv"},
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
		},
		Harness: project.RleHarnessManifest{
			Type:    project.RleTypeGym,
			Subtype: project.RleSubtypeOpenEnv,
		},
		Model: &project.RleModelDefaults{Name: &modelName},
	}
	err := verifyPublishedEnvironment(config, &environmentResource{
		Name:          "code_rl",
		Version:       "1.0.0",
		Harness:       rleHarness{Type: "Gym", Subtype: "OpenEnv"},
		SchemaVersion: &schemaVersion,
		Model:         &project.RleModelDefaults{Name: &differentModelName},
	})
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_published_environment_mismatch" {
		t.Fatalf("expected published defaults mismatch, got %v", err)
	}
}

func TestPublishGymManifestOmitsEnvironmentProtocol(t *testing.T) {
	dir := t.TempDir()
	content := `[rle]
name = "math_rl"
version = "1.0.0"

[harness]
type = "Gym"
subtype = "OpenEnv"
`
	if err := os.WriteFile(filepath.Join(dir, project.RleConfigFile), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := project.LoadRleConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(buildEnvironmentCreateRequest(config, "registry.azurecr.io/math:1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(string(body)), "protocol") {
		t.Fatalf("the request must not carry an environment protocol, got %s", body)
	}
	published := &environmentResource{
		Name: "math_rl", Version: "1.0.0", Harness: rleHarness{Type: "Gym", Subtype: "OpenEnv"},
	}
	if err := verifyPublishedEnvironment(config, published); err != nil {
		t.Fatal(err)
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
		},
		Harness: project.RleHarnessManifest{
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
