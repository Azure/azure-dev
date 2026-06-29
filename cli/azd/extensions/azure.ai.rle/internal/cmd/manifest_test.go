// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandManifestEnvUsesEnvironmentVariables(t *testing.T) {
	t.Setenv("RLE_ACR_IMAGE", "example.azurecr.io/code:latest")

	expanded, err := expandManifestEnv("image: ${RLE_ACR_IMAGE}\n")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(expanded, "image: example.azurecr.io/code:latest") {
		t.Fatalf("expected image env var to be expanded, got %q", expanded)
	}
}

func TestExpandManifestEnvRejectsMissingEnvironmentVariables(t *testing.T) {
	if _, err := expandManifestEnv("image: ${RLE_MISSING_IMAGE}\n"); err == nil {
		t.Fatal("expected missing environment variable to fail")
	}
}

func TestExpandManifestEnvIgnoresUnbracedDollarReferences(t *testing.T) {
	manifest := "# yaml-language-server: $schema=https://example/rle.schema.json\nname: code_rl\n"

	expanded, err := expandManifestEnv(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if expanded != manifest {
		t.Fatalf("expected unbraced dollar reference to be preserved, got %q", expanded)
	}
}

func TestExpandManifestEnvPreservesLiteralDollarText(t *testing.T) {
	manifest := "description: Price is $5 and schema hint is $schema\n"

	expanded, err := expandManifestEnv(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if expanded != manifest {
		t.Fatalf("expected literal dollar text to be preserved, got %q", expanded)
	}
}

func TestStateFromManifestUsesResolvedImage(t *testing.T) {
	state, err := stateFromManifest(rleManifest{
		Name:  "code_rl",
		Image: "example.azurecr.io/code:latest",
	})
	if err != nil {
		t.Fatal(err)
	}

	if state.Project != "" {
		t.Fatalf("expected manifest not to set project, got %q", state.Project)
	}
	if state.Name != "code_rl" {
		t.Fatalf("expected default environment name, got %q", state.Name)
	}
	if state.Image != "example.azurecr.io/code:latest" {
		t.Fatalf("expected resolved image, got %q", state.Image)
	}
	if state.LocalImage != "example.azurecr.io/code:latest" {
		t.Fatalf("expected local image fallback, got %q", state.LocalImage)
	}
}

func TestStateFromManifestSupportsSeparateLocalAndRegistrationImages(t *testing.T) {
	state, err := stateFromManifest(rleManifest{
		Name: "code_rl",
		Template: rleManifestTemplate{
			Kind: "openenv",
			Local: rleManifestLocal{
				Image: "example.azurecr.io/code:smoke",
			},
			Environment: rleManifestEnvironment{
				Image: "example.azurecr.io/code:deepcoder",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if state.LocalImage != "example.azurecr.io/code:smoke" {
		t.Fatalf("expected local image, got %q", state.LocalImage)
	}
	if state.Image != "example.azurecr.io/code:deepcoder" {
		t.Fatalf("expected registration image, got %q", state.Image)
	}
}

func TestStateFromManifestUsesTemplateName(t *testing.T) {
	state, err := stateFromManifest(rleManifest{
		Template: rleManifestTemplate{
			Name: "code_rl",
			Kind: "openenv",
			Local: rleManifestLocal{
				Image: "example.azurecr.io/code:smoke",
			},
			Environment: rleManifestEnvironment{
				Image: "example.azurecr.io/code:deepcoder",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Name != "code_rl" {
		t.Fatalf("expected template name, got %q", state.Name)
	}
}

func TestValidateManifestKindRejectsUnsupportedKinds(t *testing.T) {
	err := validateManifestKind(rleManifest{
		Template: rleManifestTemplate{Kind: "hosted"},
	})
	if err == nil {
		t.Fatal("expected unsupported manifest kind to fail")
	}
}

func TestNormalizeManifestUrlConvertsGitHubBlobUrl(t *testing.T) {
	got := normalizeManifestUrl("https://github.com/owner/repo/blob/main/path/to/rle.yaml")
	expected := "https://raw.githubusercontent.com/owner/repo/main/path/to/rle.yaml"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestReadManifestContentRejectsHttpUrls(t *testing.T) {
	_, err := readManifestContent(context.Background(), "http://example.com/rle.yaml")
	if err == nil {
		t.Fatal("expected http manifest URL to fail")
	}
	_, err = readManifestContent(context.Background(), "http://github.com/owner/repo/blob/main/rle.yaml")
	if err == nil {
		t.Fatal("expected http GitHub manifest URL to fail")
	}
}

func TestManifestHTTPClientRejectsHttpRedirects(t *testing.T) {
	redirectReq, err := http.NewRequest(http.MethodGet, "http://example.com/rle.yaml", nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := manifestHTTPClient().CheckRedirect(redirectReq, nil); err == nil {
		t.Fatal("expected http redirect target to fail")
	}
}

func TestLoadManifestAllowsFutureSourceFields(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "rle.yaml")
	manifest := []byte(`
name: code_rl
template:
  name: code_rl
  kind: openenv
  source:
    type: huggingface-space
    repo: openenv/echo_env
  local:
    image: example.azurecr.io/code:smoke
  environment:
    image: example.azurecr.io/code:prod
`)
	if err := os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadRleManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}

	state, err := stateFromManifest(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if state.LocalImage != "example.azurecr.io/code:smoke" {
		t.Fatalf("expected local image, got %q", state.LocalImage)
	}
	if state.Image != "example.azurecr.io/code:prod" {
		t.Fatalf("expected environment image, got %q", state.Image)
	}
}
