// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"strings"
	"testing"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func writeRunRleConfig(t *testing.T, dir string, rleType project.RleType, subtype project.RleSubtype) {
	t.Helper()

	schemaVersion := "1.0.0"
	manifest := project.RleManifest{
		Name:    "code_repair",
		Version: "1.0.0",
		Type:    rleType,
		Subtype: subtype,
	}
	// Each harness subtype carries a different way of addressing the agent:
	// BYOH is reached at a baseUrl, HostedAgent by agent name and version.
	switch {
	case rleType == project.RleTypeHarness && subtype == project.RleSubtypeBYOH:
		baseURL := "https://agent.example.test"
		manifest.BaseURL = &baseURL
	case rleType == project.RleTypeHarness && subtype == project.RleSubtypeHostedAgent:
		agentName := "code-repair-agent"
		agentVersion := "1"
		manifest.AgentName = &agentName
		manifest.AgentVersion = &agentVersion
	}

	if err := project.WriteRleConfig(dir, project.RleConfig{
		SchemaVersion: &schemaVersion,
		Rle:           manifest,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLocalRunAcceptsGymOpenEnv(t *testing.T) {
	dir := t.TempDir()
	writeRunRleConfig(t, dir, project.RleTypeGym, project.RleSubtypeOpenEnv)

	config, err := loadLocalRunConfig(&localRunFlags{source: dir})
	if err != nil {
		t.Fatalf("expected a Gym: OpenEnv manifest to be runnable, got %v", err)
	}
	if config.Rle.Name != "code_repair" {
		t.Fatalf("expected the manifest to be returned, got %#v", config.Rle)
	}
}

// A harness container serves no OpenEnv WebSocket endpoint, so run has to fail
// before it builds an image rather than at the handshake.
func TestLocalRunRejectsHarnessSubtypes(t *testing.T) {
	for _, subtype := range []project.RleSubtype{
		project.RleSubtypeBYOH,
		project.RleSubtypeHostedAgent,
	} {
		t.Run(string(subtype), func(t *testing.T) {
			dir := t.TempDir()
			writeRunRleConfig(t, dir, project.RleTypeHarness, subtype)

			_, err := loadLocalRunConfig(&localRunFlags{source: dir})
			if err == nil {
				t.Fatalf("expected Harness: %s to be rejected by azd ai rle run", subtype)
			}

			var localErr *azdext.LocalError
			if !errors.As(err, &localErr) {
				t.Fatalf("expected a LocalError, got %v", err)
			}
			if localErr.Code != "rle_run_unsupported_type" {
				t.Fatalf("expected code rle_run_unsupported_type, got %q", localErr.Code)
			}
			if localErr.Category != azdext.LocalErrorCategoryUser {
				t.Fatalf("expected a user-category error, got %q", localErr.Category)
			}
			// The message has to name what was declared, or the user cannot
			// tell which of their manifests the CLI is objecting to.
			if !strings.Contains(localErr.Message, "Harness: "+string(subtype)) {
				t.Fatalf("expected the declared type in the message, got %q", localErr.Message)
			}
			// And it has to point at the loop that does work for a harness.
			for _, want := range []string{"azd ai rle publish", "azd ai rle rollout"} {
				if !strings.Contains(localErr.Suggestion, want) {
					t.Fatalf("expected the suggestion to mention %q, got %q", want, localErr.Suggestion)
				}
			}
		})
	}
}

// The watch loop reloads the manifest through loadLocalRunConfig on every
// restart, so the gate has to hold there too rather than only on the first
// load.
func TestLocalRunGateAppliesToRestartPath(t *testing.T) {
	dir := t.TempDir()
	writeRunRleConfig(t, dir, project.RleTypeHarness, project.RleSubtypeBYOH)

	restartFlags := localRunFlags{source: dir, restart: true}
	_, err := loadLocalRunConfig(&restartFlags)
	if err == nil {
		t.Fatal("expected the restart path to reject a harness manifest")
	}

	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_run_unsupported_type" {
		t.Fatalf("expected the run type gate to reject it, got %v", err)
	}
}

func TestEnsureLocalRunSupportedReportsTypeWithoutSubtype(t *testing.T) {
	err := ensureLocalRunSupported(project.RleConfig{
		Rle: project.RleManifest{Type: project.RleTypeHarness},
	})
	if err == nil {
		t.Fatal("expected a harness manifest to be rejected")
	}

	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) {
		t.Fatalf("expected a LocalError, got %v", err)
	}
	if strings.Contains(localErr.Message, "Harness: ") {
		t.Fatalf("expected no dangling subtype separator, got %q", localErr.Message)
	}
	if !strings.Contains(localErr.Message, "Harness") {
		t.Fatalf("expected the declared type in the message, got %q", localErr.Message)
	}
}
