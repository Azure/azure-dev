// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGeneratorGenerate(t *testing.T) {
	generator := &Generator{}
	request := GenerateRequest{
		Name: "backend",
		Config: map[string]any{
			"database": map[string]any{"type": Mongo},
		},
	}

	files, err := generator.Generate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(files["main.bicep"]) == 0 || len(files["main.parameters.json"]) == 0 {
		t.Fatal("expected Bicep entry point and parameters")
	}
	if !strings.Contains(string(files["main.parameters.json"]), `"value": "backend"`) {
		t.Fatal("expected infrastructure entry identity in parameters")
	}
}

func TestGeneratorRejectsInvalidConfig(t *testing.T) {
	_, err := (&Generator{}).Generate(t.Context(), GenerateRequest{
		Config: map[string]any{
			"database": map[string]any{"type": Mongo, "unknown": true},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected provider config validation error, got %v", err)
	}
}

func TestGeneratorHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := (&Generator{}).Generate(ctx, GenerateRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestGeneratorRejectsUnsupportedExistingDatabaseLinks(t *testing.T) {
	for _, resourceType := range []string{Redis, Mongo} {
		t.Run(resourceType, func(t *testing.T) {
			_, err := Decode(map[string]any{
				"api":      map[string]any{"type": ContainerApp, "port": 8080, "uses": []string{"database"}},
				"database": map[string]any{"type": resourceType, "existing": true},
			})
			if err == nil || !strings.Contains(err.Error(), "connection bindings are not supported") {
				t.Fatalf("expected unsupported existing database link error, got %v", err)
			}
		})
	}
}

func TestGeneratorNormalizesBackendEnvironmentKeys(t *testing.T) {
	files, err := Generate("backend", Config{
		"api-service": {Type: ContainerApp, Port: 8080},
		"web": {
			Type: AppService, Port: 8000, Uses: []string{"api-service"},
			Runtime: AppServiceRuntime{Stack: "python", Version: "3.12"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := string(files["resources.bicep"])
	if !strings.Contains(source, "API_SERVICE_BASE_URL:") || strings.Contains(source, "API-SERVICE_BASE_URL") {
		t.Fatal("backend environment keys must be valid Bicep identifiers")
	}
	config := Config{
		"api-service": {Type: ContainerApp, Port: 8080},
		"web": {
			Type: ContainerApp, Port: 8000, Uses: []string{"api-service"},
			Env: []ServiceEnvVar{{Name: "API_SERVICE_BASE_URL", Value: "override"}},
		},
	}
	if err := config.Validate(); err == nil {
		t.Fatal("expected collision with normalized backend environment key")
	}
}
