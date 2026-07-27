// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestServiceErrorSuggestionShowsCurrentEndpoint(t *testing.T) {
	t.Setenv("RLE_ENDPOINT", "https://rle.example.test")

	err := serviceError(errors.New("dial tcp failed"))
	var serviceErr *azdext.ServiceError
	if !errors.As(err, &serviceErr) {
		t.Fatalf("expected ServiceError, got %T", err)
	}

	for _, expected := range []string{
		"Ensure the RLE control plane is running and reachable.",
		"Trying at https://rle.example.test;",
		"RLE_ENDPOINT=<endpoint>",
	} {
		if !strings.Contains(serviceErr.Suggestion, expected) {
			t.Fatalf("expected suggestion to contain %q, got %q", expected, serviceErr.Suggestion)
		}
	}
}

func TestResolveDeployStateUsesFoundryProjectEndpointEnvironment(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	t.Setenv(foundryProjectEndpointEnvVar, "https://ACCOUNT.services.ai.azure.com/api/projects/project-from-env/")

	state, initialized, err := resolveDeployState(&rleDeployFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if initialized {
		t.Fatal("expected no saved state")
	}
	if state.ProjectEndpoint != "https://account.services.ai.azure.com/api/projects/project-from-env" {
		t.Fatalf("expected normalized project endpoint, got %q", state.ProjectEndpoint)
	}
}

func TestResolveDeployStateIgnoresSavedProjectEndpointFallback(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)
	if err := saveRleState(rleState{
		Name:            "saved-env",
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/saved-project",
	}); err != nil {
		t.Fatal(err)
	}

	state, initialized, err := resolveDeployState(&rleDeployFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if !initialized {
		t.Fatal("expected saved state")
	}
	if state.ProjectEndpoint != "" {
		t.Fatalf("expected saved project endpoint fallback to be ignored, got %q", state.ProjectEndpoint)
	}
}

func TestProjectNameFromFoundryEndpoint(t *testing.T) {
	projectName, err := projectNameFromFoundryEndpoint(
		"https://account.services.ai.azure.com/api/projects/my-project",
	)
	if err != nil {
		t.Fatal(err)
	}
	if projectName != "my-project" {
		t.Fatalf("expected project name from endpoint, got %q", projectName)
	}
}

func TestProjectEndpointRequiresProjectPath(t *testing.T) {
	_, err := normalizeFoundryProjectEndpoint("https://account.services.ai.azure.com/api/not-projects/my-project")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_invalid_project_endpoint" {
		t.Fatalf("expected invalid endpoint code, got %q", localErr.Code)
	}
}
