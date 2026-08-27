// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultPromptAgentSettings_PublicDefaults asserts the defaults point at
// the public managed prompt-agent endpoint with placeholder workspace tuple.
func TestDefaultPromptAgentSettings_PublicDefaults(t *testing.T) {
	s := DefaultPromptAgentSettings()
	if s.BaseURL != DefaultPromptBaseURL {
		t.Errorf("BaseURL: got %q, want %q", s.BaseURL, DefaultPromptBaseURL)
	}
	if s.SubscriptionID != DefaultPromptSubscriptionID {
		t.Errorf("SubscriptionID: got %q, want %q", s.SubscriptionID, DefaultPromptSubscriptionID)
	}
	if s.ResourceGroup != DefaultPromptResourceGroup {
		t.Errorf("ResourceGroup: got %q, want %q", s.ResourceGroup, DefaultPromptResourceGroup)
	}
	if s.Workspace != DefaultPromptWorkspace {
		t.Errorf("Workspace: got %q, want %q", s.Workspace, DefaultPromptWorkspace)
	}
	if s.EffectiveAPIVersion() != DefaultPromptAPIVersion {
		t.Errorf("api-version: got %q, want %q", s.EffectiveAPIVersion(), DefaultPromptAPIVersion)
	}
	if s.EffectiveModelEndpoint() != DefaultPromptModelEndpoint {
		t.Errorf("model endpoint: got %q, want %q", s.EffectiveModelEndpoint(), DefaultPromptModelEndpoint)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("default settings should validate: %v", err)
	}
}

// TestPromptAgentSettings_Validate_MissingFields asserts each required field is
// reported when empty.
func TestPromptAgentSettings_Validate_MissingFields(t *testing.T) {
	cases := map[string]PromptAgentSettings{
		"missing baseUrl":        {SubscriptionID: "s", ResourceGroup: "r", Workspace: "w"},
		"missing subscriptionId": {BaseURL: "https://ai.azure.com", ResourceGroup: "r", Workspace: "w"},
		"missing resourceGroup":  {BaseURL: "https://ai.azure.com", SubscriptionID: "s", Workspace: "w"},
		"missing workspace":      {BaseURL: "https://ai.azure.com", SubscriptionID: "s", ResourceGroup: "r"},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", name)
			}
		})
	}
}

// TestPromptAgentSettings_EffectiveDefaults asserts the effective getters fall
// back to package defaults when unset and honor explicit values.
func TestPromptAgentSettings_EffectiveDefaults(t *testing.T) {
	s := &PromptAgentSettings{}
	if s.EffectiveAPIVersion() != DefaultPromptAPIVersion {
		t.Errorf("api-version fallback: got %q", s.EffectiveAPIVersion())
	}
	if s.EffectiveModelEndpoint() != DefaultPromptModelEndpoint {
		t.Errorf("model endpoint fallback: got %q", s.EffectiveModelEndpoint())
	}
	s.APIVersion = "v2"
	s.ModelEndpoint = "https://custom"
	if s.EffectiveAPIVersion() != "v2" {
		t.Errorf("api-version: got %q, want v2", s.EffectiveAPIVersion())
	}
	if s.EffectiveModelEndpoint() != "https://custom" {
		t.Errorf("model endpoint: got %q, want https://custom", s.EffectiveModelEndpoint())
	}
}

// TestPromptAgentSettings_ApplyEnvOverrides asserts environment variables take
// precedence over stored values.
func TestPromptAgentSettings_ApplyEnvOverrides(t *testing.T) {
	s := DefaultPromptAgentSettings()
	t.Setenv(PromptBaseURLEnvVar, "http://localhost:9999")
	t.Setenv(PromptSubscriptionEnvVar, "sub-override")
	t.Setenv(PromptResourceGroupEnvVar, "rg-override")
	t.Setenv(PromptWorkspaceEnvVar, "ws-override")
	t.Setenv(PromptAPIVersionEnvVar, "v9")
	t.Setenv(PromptModelEndpointEnvVar, "https://model-override")

	s.ApplyEnvOverrides()

	if s.BaseURL != "http://localhost:9999" {
		t.Errorf("BaseURL override: got %q", s.BaseURL)
	}
	if s.SubscriptionID != "sub-override" {
		t.Errorf("SubscriptionID override: got %q", s.SubscriptionID)
	}
	if s.ResourceGroup != "rg-override" {
		t.Errorf("ResourceGroup override: got %q", s.ResourceGroup)
	}
	if s.Workspace != "ws-override" {
		t.Errorf("Workspace override: got %q", s.Workspace)
	}
	if s.EffectiveAPIVersion() != "v9" {
		t.Errorf("APIVersion override: got %q", s.EffectiveAPIVersion())
	}
	if s.EffectiveModelEndpoint() != "https://model-override" {
		t.Errorf("ModelEndpoint override: got %q", s.EffectiveModelEndpoint())
	}
}

// TestExpandPromptAgentSettings asserts the ${VAR} references `azd ai agent
// init` writes into the promptAgent block resolve against the azd environment,
// that unset references collapse to "" (so overlay leaves the defaults in
// place), and that literal values are passed through untouched.
func TestExpandPromptAgentSettings(t *testing.T) {
	// Not parallel: the unset case pins the referenced variables to empty via
	// t.Setenv so a developer who exports them locally still sees the unset
	// behavior (expansion falls back to the process environment).
	const endpoint = "https://acct.services.ai.azure.com/api/projects/p1"
	refs := &PromptAgentSettings{
		BaseURL:         "${AZD_MANAGED_AGENT_BASE_URL}",
		SubscriptionID:  "${AZURE_SUBSCRIPTION_ID}",
		ResourceGroup:   "${AZURE_RESOURCE_GROUP}",
		Workspace:       "${AZURE_AI_WORKSPACE}",
		ProjectEndpoint: "${AZURE_AI_PROJECT_ENDPOINT}",
	}

	t.Run("resolves references from the azd environment", func(t *testing.T) {
		got, err := expandPromptAgentSettings(refs, map[string]string{
			"AZD_MANAGED_AGENT_BASE_URL": "https://harness.example",
			"AZURE_SUBSCRIPTION_ID":      "sub-1",
			"AZURE_RESOURCE_GROUP":       "rg-1",
			"AZURE_AI_WORKSPACE":         "acct@p1@AML",
			"AZURE_AI_PROJECT_ENDPOINT":  endpoint,
		})

		require.NoError(t, err)
		assert.Equal(t, "https://harness.example", got.BaseURL)
		assert.Equal(t, "sub-1", got.SubscriptionID)
		assert.Equal(t, "rg-1", got.ResourceGroup)
		assert.Equal(t, "acct@p1@AML", got.Workspace)
		assert.Equal(t, endpoint, got.ProjectEndpoint)
	})

	t.Run("unset references fall back to the defaults", func(t *testing.T) {
		for _, name := range []string{
			"AZD_MANAGED_AGENT_BASE_URL",
			"AZURE_SUBSCRIPTION_ID",
			"AZURE_RESOURCE_GROUP",
			"AZURE_AI_WORKSPACE",
			"AZURE_AI_PROJECT_ENDPOINT",
		} {
			t.Setenv(name, "")
		}

		got, err := expandPromptAgentSettings(refs, nil)
		require.NoError(t, err)
		assert.Empty(t, got.BaseURL)
		assert.Empty(t, got.Workspace)

		// overlay must not blank the defaults with the empty expansions.
		settings := DefaultPromptAgentSettings()
		settings.overlay(got)
		assert.Equal(t, DefaultPromptBaseURL, settings.BaseURL)
		assert.Equal(t, DefaultPromptWorkspace, settings.Workspace)
	})

	t.Run("literal values are preserved", func(t *testing.T) {
		got, err := expandPromptAgentSettings(&PromptAgentSettings{
			BaseURL:   "https://literal.example",
			Workspace: "acct@p1@AML",
		}, nil)

		require.NoError(t, err)
		assert.Equal(t, "https://literal.example", got.BaseURL)
		assert.Equal(t, "acct@p1@AML", got.Workspace)
	})
}

// TestResolvePromptAgentSettings asserts the shared resolution deploy and the
// lifecycle commands (show/invoke/list/delete) both run: ${VAR} references are
// expanded before anything consumes them, and unset fields fall back to the
// package defaults. The projectEndpoint case is a regression guard — leaving it
// unexpanded surfaced as `projectEndpoint "${AZURE_AI_PROJECT_ENDPOINT}" is not
// a valid absolute URL` once the client tried to split it.
func TestResolvePromptAgentSettings(t *testing.T) {
	const endpoint = "https://acct.services.ai.azure.com/api/projects/p1"

	t.Run("expands references and applies defaults", func(t *testing.T) {
		got, err := ResolvePromptAgentSettings(&PromptAgentSettings{
			SubscriptionID:  "${AZURE_SUBSCRIPTION_ID}",
			ResourceGroup:   "${AZURE_RESOURCE_GROUP}",
			ProjectEndpoint: "${AZURE_AI_PROJECT_ENDPOINT}",
		}, map[string]string{
			"AZURE_SUBSCRIPTION_ID":     "sub-1",
			"AZURE_RESOURCE_GROUP":      "rg-1",
			"AZURE_AI_PROJECT_ENDPOINT": endpoint,
		})

		require.NoError(t, err)
		assert.Equal(t, endpoint, got.ProjectEndpoint)
		assert.Equal(t, "sub-1", got.SubscriptionID)
		assert.Equal(t, "rg-1", got.ResourceGroup)
		// Not configured, so the defaults must survive the overlay.
		assert.Equal(t, DefaultPromptBaseURL, got.BaseURL)
		assert.Equal(t, DefaultPromptWorkspace, got.Workspace)
		assert.Equal(t, DefaultPromptAPIVersion, got.EffectiveAPIVersion())
	})

	t.Run("literal values are preserved", func(t *testing.T) {
		got, err := ResolvePromptAgentSettings(&PromptAgentSettings{
			ProjectEndpoint: endpoint,
		}, nil)

		require.NoError(t, err)
		assert.Equal(t, endpoint, got.ProjectEndpoint)
	})

	t.Run("nil config yields the defaults", func(t *testing.T) {
		got, err := ResolvePromptAgentSettings(nil, nil)

		require.NoError(t, err)
		assert.Equal(t, DefaultPromptBaseURL, got.BaseURL)
		assert.Equal(t, DefaultPromptWorkspace, got.Workspace)
	})
}

// TestNewPromptAgentClient_BuildsClient asserts a client builds from valid
// settings (no-auth path to avoid requiring an Azure login in tests).
func TestNewPromptAgentClient_BuildsClient(t *testing.T) {
	t.Setenv(PromptNoAuthEnvVar, "true")
	s := DefaultPromptAgentSettings()
	client, err := NewPromptAgentClient(&s)
	if err != nil {
		t.Fatalf("NewPromptAgentClient: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

// TestPromptAgentResponsesEndpoint asserts the workspace-rooted Responses URL is
// assembled correctly.
func TestPromptAgentResponsesEndpoint(t *testing.T) {
	s := PromptAgentSettings{
		BaseURL:        "http://localhost:5000",
		SubscriptionID: "sub-1",
		ResourceGroup:  "rg-x",
		Workspace:      "ws-y",
		APIVersion:     "v1",
	}
	got := promptAgentResponsesEndpoint(&s)
	want := "http://localhost:5000/agents/v2.0/subscriptions/sub-1/resourceGroups/rg-x/" +
		"providers/Microsoft.MachineLearningServices/workspaces/ws-y/openai/responses?api-version=v1"
	if got != want {
		t.Errorf("endpoint:\n got %q\nwant %q", got, want)
	}
}

// TestPromptAgentResponsesEndpoint_ProjectEndpoint asserts the Responses URL is
// built off the Foundry project data-plane endpoint when one is configured.
func TestPromptAgentResponsesEndpoint_ProjectEndpoint(t *testing.T) {
	s := PromptAgentSettings{
		ProjectEndpoint: "https://acct.services.ai.azure.com/api/projects/proj",
		APIVersion:      "v1",
	}
	got := promptAgentResponsesEndpoint(&s)
	want := "https://acct.services.ai.azure.com/api/projects/proj/openai/v1/responses"
	if got != want {
		t.Errorf("endpoint:\n got %q\nwant %q", got, want)
	}
}

// TestOverlayAzdProjectEnv_FillsDefaultsOnly asserts that only fields still at
// their package default are overlaid from the azd environment, and real values
// resolved at init time are preserved.
func TestOverlayAzdProjectEnv_FillsDefaultsOnly(t *testing.T) {
	env := map[string]string{
		"AZURE_SUBSCRIPTION_ID": "real-sub",
		"AZURE_RESOURCE_GROUP":  "real-rg",
		"AZURE_AI_PROJECT_NAME": "real-proj",
		"AZURE_AI_ACCOUNT_NAME": "myacct",
	}

	t.Run("defaults are filled from env", func(t *testing.T) {
		s := DefaultPromptAgentSettings()
		s.OverlayAzdProjectEnv(env)
		if s.SubscriptionID != "real-sub" {
			t.Errorf("SubscriptionID: got %q", s.SubscriptionID)
		}
		if s.ResourceGroup != "real-rg" {
			t.Errorf("ResourceGroup: got %q", s.ResourceGroup)
		}
		if s.Workspace != "real-proj" {
			t.Errorf("Workspace: got %q", s.Workspace)
		}
		if s.ModelEndpoint != "https://myacct.services.ai.azure.com" {
			t.Errorf("ModelEndpoint: got %q", s.ModelEndpoint)
		}
	})

	t.Run("non-default values are preserved", func(t *testing.T) {
		s := PromptAgentSettings{
			BaseURL:        "https://harness.example",
			SubscriptionID: "chosen-sub",
			ResourceGroup:  "chosen-rg",
			Workspace:      "chosen-ws",
			ModelEndpoint:  "https://chosen.services.ai.azure.com",
		}
		s.OverlayAzdProjectEnv(env)
		if s.SubscriptionID != "chosen-sub" || s.ResourceGroup != "chosen-rg" ||
			s.Workspace != "chosen-ws" || s.ModelEndpoint != "https://chosen.services.ai.azure.com" {
			t.Errorf("non-default values should be preserved, got %+v", s)
		}
	})

	t.Run("nil env is a no-op", func(t *testing.T) {
		s := DefaultPromptAgentSettings()
		s.OverlayAzdProjectEnv(nil)
		if s.Workspace != DefaultPromptWorkspace {
			t.Errorf("nil env should not change settings")
		}
	})

	t.Run("env without a project name is a no-op", func(t *testing.T) {
		// No AZURE_AI_PROJECT_NAME means no provisioned project — the local-dev
		// fake tuple must be preserved even if a subscription id leaks in.
		s := DefaultPromptAgentSettings()
		s.OverlayAzdProjectEnv(map[string]string{"AZURE_SUBSCRIPTION_ID": "leaked-sub"})
		if s.SubscriptionID != DefaultPromptSubscriptionID || s.Workspace != DefaultPromptWorkspace {
			t.Errorf("settings should be untouched without a project name, got %+v", s)
		}
	})
}

func TestOverlayPromptSettingsFromProjectResourceID(t *testing.T) {
	tests := []struct {
		name               string
		settings           PromptAgentSettings
		env                map[string]string
		wantApplied        bool
		wantErr            bool
		wantCode           string
		wantSubscriptionID string
		wantResourceGroup  string
		wantWorkspace      string
		wantModelEndpoint  string
	}{
		{
			name:     "applies from valid project id",
			settings: DefaultPromptAgentSettings(),
			env: map[string]string{
				"AZURE_AI_PROJECT_ID": "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.CognitiveServices/accounts/acct-1/projects/proj-1",
			},
			wantApplied:        true,
			wantSubscriptionID: "sub-1",
			wantResourceGroup:  "rg-1",
			wantWorkspace:      "acct-1@proj-1@AML",
			wantModelEndpoint:  "https://acct-1.services.ai.azure.com",
		},
		{
			name: "keeps explicit model endpoint",
			settings: PromptAgentSettings{
				BaseURL:        DefaultPromptBaseURL,
				SubscriptionID: "custom-sub",
				ResourceGroup:  "custom-rg",
				Workspace:      "custom-ws",
				ModelEndpoint:  "https://custom.services.ai.azure.com",
			},
			env: map[string]string{
				"AZURE_AI_PROJECT_ID": "/subscriptions/sub-2/resourceGroups/rg-2/providers/Microsoft.CognitiveServices/accounts/acct-2/projects/proj-2",
			},
			wantApplied:        true,
			wantSubscriptionID: "sub-2",
			wantResourceGroup:  "rg-2",
			wantWorkspace:      "acct-2@proj-2@AML",
			wantModelEndpoint:  "https://custom.services.ai.azure.com",
		},
		{
			name:          "no project id means no-op",
			settings:      DefaultPromptAgentSettings(),
			env:           map[string]string{},
			wantApplied:   false,
			wantWorkspace: DefaultPromptWorkspace,
		},
		{
			name:     "invalid project id returns validation error",
			settings: DefaultPromptAgentSettings(),
			env: map[string]string{
				"AZURE_AI_PROJECT_ID": "not-a-resource-id",
			},
			wantErr:  true,
			wantCode: "invalid_ai_project_id",
		},
		{
			name:     "non project resource id returns validation error",
			settings: DefaultPromptAgentSettings(),
			env: map[string]string{
				"AZURE_AI_PROJECT_ID": "/subscriptions/sub-1/resourceGroups/rg-1/providers/Microsoft.CognitiveServices/accounts/acct-1",
			},
			wantErr:  true,
			wantCode: "invalid_ai_project_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.settings
			applied, err := overlayPromptSettingsFromProjectResourceID(&s, tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				if !ok {
					t.Fatalf("expected *azdext.LocalError, got %T", err)
				}
				if tt.wantCode != "" && localErr.Code != tt.wantCode {
					t.Fatalf("error code: got %q, want %q", localErr.Code, tt.wantCode)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if applied != tt.wantApplied {
				t.Fatalf("applied: got %t, want %t", applied, tt.wantApplied)
			}
			if tt.wantSubscriptionID != "" && s.SubscriptionID != tt.wantSubscriptionID {
				t.Fatalf("SubscriptionID: got %q, want %q", s.SubscriptionID, tt.wantSubscriptionID)
			}
			if tt.wantResourceGroup != "" && s.ResourceGroup != tt.wantResourceGroup {
				t.Fatalf("ResourceGroup: got %q, want %q", s.ResourceGroup, tt.wantResourceGroup)
			}
			if tt.wantWorkspace != "" && s.Workspace != tt.wantWorkspace {
				t.Fatalf("Workspace: got %q, want %q", s.Workspace, tt.wantWorkspace)
			}
			if tt.wantModelEndpoint != "" && s.ModelEndpoint != tt.wantModelEndpoint {
				t.Fatalf("ModelEndpoint: got %q, want %q", s.ModelEndpoint, tt.wantModelEndpoint)
			}
		})
	}
}

// TestResolvePromptTargetFromEnv_ProjectEndpoint asserts that the Foundry
// project data-plane endpoint is resolved (config first, env fallback), that
// the api-version is normalized to v1, and that the model endpoint is derived
// from the account host.
func TestResolvePromptTargetFromEnv_ProjectEndpoint(t *testing.T) {
	t.Run("from environment when config is empty", func(t *testing.T) {
		s := DefaultPromptAgentSettings()
		env := map[string]string{
			"AZURE_AI_PROJECT_NAME":     "proj-1",
			"AZURE_AI_PROJECT_ENDPOINT": "https://acct-1.services.ai.azure.com/api/projects/proj-1",
		}
		applied, err := ResolvePromptTargetFromEnv(&s, env)
		if err != nil {
			t.Fatalf("ResolvePromptTargetFromEnv: %v", err)
		}
		if !applied {
			t.Fatalf("expected project-scoped target to be applied")
		}
		if s.ProjectEndpoint != "https://acct-1.services.ai.azure.com/api/projects/proj-1" {
			t.Errorf("ProjectEndpoint: got %q", s.ProjectEndpoint)
		}
		if s.EffectiveAPIVersion() != ProjectEndpointAPIVersion {
			t.Errorf("APIVersion: got %q, want %q", s.EffectiveAPIVersion(), ProjectEndpointAPIVersion)
		}
		if s.ModelEndpoint != "https://acct-1.services.ai.azure.com" {
			t.Errorf("ModelEndpoint: got %q", s.ModelEndpoint)
		}
	})

	t.Run("config value takes precedence over environment", func(t *testing.T) {
		s := DefaultPromptAgentSettings()
		s.ProjectEndpoint = "https://config-acct.services.ai.azure.com/api/projects/config-proj"
		env := map[string]string{
			"AZURE_AI_PROJECT_NAME":     "proj-1",
			"AZURE_AI_PROJECT_ENDPOINT": "https://env-acct.services.ai.azure.com/api/projects/env-proj",
		}
		if _, err := ResolvePromptTargetFromEnv(&s, env); err != nil {
			t.Fatalf("ResolvePromptTargetFromEnv: %v", err)
		}
		if s.ProjectEndpoint != "https://config-acct.services.ai.azure.com/api/projects/config-proj" {
			t.Errorf("ProjectEndpoint should keep config value, got %q", s.ProjectEndpoint)
		}
	})

	// A greenfield `azd up` provisions the project through the microsoft.foundry
	// provider, which writes FOUNDRY_PROJECT_ENDPOINT (not the older
	// AZURE_AI_PROJECT_ENDPOINT). Without this fallback the deploy drops to the
	// legacy workspace-rooted harness route and gets a 404.
	t.Run("falls back to FOUNDRY_PROJECT_ENDPOINT", func(t *testing.T) {
		s := DefaultPromptAgentSettings()
		env := map[string]string{
			"AZURE_AI_PROJECT_NAME":    "proj-1",
			"FOUNDRY_PROJECT_ENDPOINT": "https://acct-1.services.ai.azure.com/api/projects/proj-1",
		}
		applied, err := ResolvePromptTargetFromEnv(&s, env)
		if err != nil {
			t.Fatalf("ResolvePromptTargetFromEnv: %v", err)
		}
		if !applied {
			t.Fatalf("expected project-scoped target to be applied")
		}
		if s.ProjectEndpoint != "https://acct-1.services.ai.azure.com/api/projects/proj-1" {
			t.Errorf("ProjectEndpoint: got %q", s.ProjectEndpoint)
		}
		if s.EffectiveAPIVersion() != ProjectEndpointAPIVersion {
			t.Errorf("APIVersion: got %q, want %q", s.EffectiveAPIVersion(), ProjectEndpointAPIVersion)
		}
	})

	t.Run("AZURE_AI_PROJECT_ENDPOINT wins over FOUNDRY_PROJECT_ENDPOINT", func(t *testing.T) {
		s := DefaultPromptAgentSettings()
		env := map[string]string{
			"AZURE_AI_PROJECT_NAME":     "proj-1",
			"AZURE_AI_PROJECT_ENDPOINT": "https://azure-acct.services.ai.azure.com/api/projects/proj-1",
			"FOUNDRY_PROJECT_ENDPOINT":  "https://foundry-acct.services.ai.azure.com/api/projects/proj-1",
		}
		if _, err := ResolvePromptTargetFromEnv(&s, env); err != nil {
			t.Fatalf("ResolvePromptTargetFromEnv: %v", err)
		}
		if s.ProjectEndpoint != "https://azure-acct.services.ai.azure.com/api/projects/proj-1" {
			t.Errorf("ProjectEndpoint: got %q", s.ProjectEndpoint)
		}
	})
}
