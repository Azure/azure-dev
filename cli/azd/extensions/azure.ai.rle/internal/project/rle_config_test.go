// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestWriteAndLoadRleConfigCanonicalizesHostedAgentManifest(t *testing.T) {
	dir := t.TempDir()
	agentName := " support-agent "
	agentVersion := "12"
	config := RleConfig{
		Rle: RleManifest{
			Name:         "support_agent",
			Version:      "1.0.0",
			Type:         RleTypeAgent,
			Subtype:      RleSubtypeHostedAgent,
			AgentName:    &agentName,
			AgentVersion: &agentVersion,
		},
	}

	if err := WriteRleConfig(dir, config); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, RleConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`name = 'support_agent'`,
		`version = '1.0.0'`,
		`type = 'Agent'`,
		`subtype = 'HostedAgent'`,
		`agentName = 'support-agent'`,
		`agentVersion = '12'`,
	} {
		if !strings.Contains(string(data), expected) {
			t.Fatalf("expected config to contain %q, got:\n%s", expected, data)
		}
	}
	if strings.Contains(string(data), "kind") {
		t.Fatalf("expected canonical type/subtype fields, got:\n%s", data)
	}

	loaded, err := LoadRleConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Rle.AgentName == nil || *loaded.Rle.AgentName != "support-agent" {
		t.Fatalf("expected normalized HostedAgent name, got %#v", loaded.Rle.AgentName)
	}
	if loaded.Rle.AgentVersion == nil || *loaded.Rle.AgentVersion != "12" {
		t.Fatalf("expected HostedAgent version, got %#v", loaded.Rle.AgentVersion)
	}
}

func TestNormalizeRleConfigEnforcesControlPlaneTypeContract(t *testing.T) {
	tests := []struct {
		name     string
		manifest RleManifest
		wantCode string
		wantURL  string
	}{
		{
			name: "gym openenv",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
				Type:    "gym",
				Subtype: "openenv",
			},
		},
		{
			name: "hosted agent released version",
			manifest: RleManifest{
				Name:         "support_agent",
				Version:      "1.0.0",
				Type:         RleTypeAgent,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    stringPointer("support-agent"),
				AgentVersion: stringPointer("12"),
			},
		},
		{
			name: "hosted agent draft version",
			manifest: RleManifest{
				Name:         "support_agent",
				Version:      "1.0.0",
				Type:         RleTypeAgent,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    stringPointer("support-agent"),
				AgentVersion: stringPointer("draft-1767225600"),
			},
		},
		{
			name: "byoa",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
				Type:    RleTypeAgent,
				Subtype: RleSubtypeBYOA,
				BaseURL: stringPointer("https://Agent.Example.com/rle/"),
			},
			wantURL: "https://agent.example.com/rle/",
		},
		{
			name: "type required",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
				Subtype: RleSubtypeOpenEnv,
			},
			wantCode: "rle_manifest_type_invalid",
		},
		{
			name: "subtype required",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
				Type:    RleTypeGym,
			},
			wantCode: "rle_manifest_subtype_invalid",
		},
		{
			name: "gym rejects agent configuration",
			manifest: RleManifest{
				Name:      "code_rl",
				Version:   "1.0.0",
				Type:      RleTypeGym,
				Subtype:   RleSubtypeOpenEnv,
				AgentName: stringPointer("support-agent"),
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "gym rejects agent subtype",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0.0",
				Type:    RleTypeGym,
				Subtype: RleSubtypeHostedAgent,
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "hosted agent requires valid version",
			manifest: RleManifest{
				Name:         "support_agent",
				Version:      "1.0.0",
				Type:         RleTypeAgent,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    stringPointer("support-agent"),
				AgentVersion: stringPointer("v12"),
			},
			wantCode: "rle_agent_version_invalid",
		},
		{
			name: "hosted agent rejects base url",
			manifest: RleManifest{
				Name:         "support_agent",
				Version:      "1.0.0",
				Type:         RleTypeAgent,
				Subtype:      RleSubtypeHostedAgent,
				AgentName:    stringPointer("support-agent"),
				AgentVersion: stringPointer("12"),
				BaseURL:      stringPointer("https://agent.example.com"),
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "byoa rejects hosted fields",
			manifest: RleManifest{
				Name:      "customer_agent",
				Version:   "1.0.0",
				Type:      RleTypeAgent,
				Subtype:   RleSubtypeBYOA,
				AgentName: stringPointer("support-agent"),
				BaseURL:   stringPointer("https://agent.example.com"),
			},
			wantCode: "rle_manifest_type_configuration_invalid",
		},
		{
			name: "byoa requires https",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
				Type:    RleTypeAgent,
				Subtype: RleSubtypeBYOA,
				BaseURL: stringPointer("http://agent.example.com"),
			},
			wantCode: "rle_agent_base_url_invalid",
		},
		{
			name: "byoa rejects credentials",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
				Type:    RleTypeAgent,
				Subtype: RleSubtypeBYOA,
				BaseURL: stringPointer("https://user@agent.example.com"),
			},
			wantCode: "rle_agent_base_url_invalid",
		},
		{
			name: "byoa rejects query",
			manifest: RleManifest{
				Name:    "customer_agent",
				Version: "1.0.0",
				Type:    RleTypeAgent,
				Subtype: RleSubtypeBYOA,
				BaseURL: stringPointer("https://agent.example.com?token=secret"),
			},
			wantCode: "rle_agent_base_url_invalid",
		},
		{
			name: "rle version must match service semantics",
			manifest: RleManifest{
				Name:    "code_rl",
				Version: "1.0",
				Type:    RleTypeGym,
				Subtype: RleSubtypeOpenEnv,
			},
			wantCode: "rle_manifest_version_invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := NormalizeRleConfig(RleConfig{Rle: test.manifest})
			if test.wantCode == "" {
				if err != nil {
					t.Fatal(err)
				}
				if config.Rle.Type == "" || config.Rle.Subtype == "" {
					t.Fatalf("expected canonical type/subtype, got %#v", config.Rle)
				}
				if test.wantURL != "" && (config.Rle.BaseURL == nil || *config.Rle.BaseURL != test.wantURL) {
					t.Fatalf("expected base URL %q, got %#v", test.wantURL, config.Rle.BaseURL)
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
`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadRleConfig(dir)
	var localErr *azdext.LocalError
	if !errors.As(err, &localErr) || localErr.Code != "rle_manifest_invalid" {
		t.Fatalf("expected legacy kind manifest to be rejected, got %v", err)
	}
}

func TestVersionBumpForManifestVersion(t *testing.T) {
	tests := []struct {
		name    string
		current string
		desired string
		want    string
		wantErr string
	}{
		{name: "initial", desired: "1.0.0", want: "Major"},
		{name: "major", current: "1.0.0", desired: "2.0.0", want: "Major"},
		{name: "minor", current: "1.0.0", desired: "1.1.0", want: "Minor"},
		{name: "patch", current: "1.0.0", desired: "1.0.1", want: "Patch"},
		{name: "invalid initial", desired: "0.1.0", wantErr: "rle_manifest_initial_version_invalid"},
		{name: "skipped patch", current: "1.0.0", desired: "1.0.2", wantErr: "rle_manifest_version_not_next"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := VersionBumpForManifestVersion(test.current, test.desired)
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if got != test.want {
					t.Fatalf("expected %q, got %q", test.want, got)
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

func stringPointer(value string) *string {
	return &value
}
