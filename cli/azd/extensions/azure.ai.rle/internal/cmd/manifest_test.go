// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"
)

func TestExpandManifestEnvUsesEnvironmentVariables(t *testing.T) {
	t.Setenv("RLE_PROJECT_NAME", "demo")
	t.Setenv("RLE_ACR_IMAGE", "example.azurecr.io/code:latest")

	expanded, err := expandManifestEnv("project: ${RLE_PROJECT_NAME}\nimage: ${RLE_ACR_IMAGE}\n")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(expanded, "project: demo") {
		t.Fatalf("expected project env var to be expanded, got %q", expanded)
	}
	if !strings.Contains(expanded, "image: example.azurecr.io/code:latest") {
		t.Fatalf("expected image env var to be expanded, got %q", expanded)
	}
}

func TestExpandManifestEnvRejectsMissingEnvironmentVariables(t *testing.T) {
	if _, err := expandManifestEnv("project: ${RLE_MISSING_PROJECT}\n"); err == nil {
		t.Fatal("expected missing environment variable to fail")
	}
}

func TestStateFromManifestUsesResolvedImage(t *testing.T) {
	state, err := stateFromManifest(rleManifest{
		Name:     "code_rl",
		Project:  "demo",
		Endpoint: "http://localhost:5000",
		Image:    "example.azurecr.io/code:latest",
	})
	if err != nil {
		t.Fatal(err)
	}

	if state.Project != "demo" {
		t.Fatalf("expected project demo, got %q", state.Project)
	}
	if state.Endpoint != "http://localhost:5000" {
		t.Fatalf("expected endpoint, got %q", state.Endpoint)
	}
	if state.Name != "code_rl" {
		t.Fatalf("expected default environment name, got %q", state.Name)
	}
	if state.Image != "example.azurecr.io/code:latest" {
		t.Fatalf("expected resolved image, got %q", state.Image)
	}
}
