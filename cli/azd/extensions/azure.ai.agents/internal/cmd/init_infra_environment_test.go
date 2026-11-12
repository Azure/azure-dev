// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore Chdir

package cmd

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type infraEjectEnvironmentServer struct {
	testEnvironmentServiceServer
	configByEnvironment map[string]map[string][]byte
	configReads         []*azdext.GetConfigRequest
}

func (s *infraEjectEnvironmentServer) GetConfig(
	_ context.Context, req *azdext.GetConfigRequest,
) (*azdext.GetConfigResponse, error) {
	s.configReads = append(s.configReads, req)
	value, found := s.configByEnvironment[req.GetEnvName()][req.GetPath()]
	return &azdext.GetConfigResponse{Value: value, Found: found}, nil
}

func TestReadInfraEjectEnvironmentSelection(t *testing.T) {
	for _, tt := range []struct {
		name, selected, defaultState, selectedState string
		enabled, blocked, missing                   bool
	}{
		{name: "saved default"},
		{name: "default selector ignored", selected: "dev", defaultState: "selector"},
		{name: "default binding ignored", selected: "dev", defaultState: "canonical"},
		{name: "default pending ignored", selected: "dev", defaultState: "pending"},
		{name: "selected selector gated", selected: "dev", selectedState: "selector", blocked: true},
		{name: "selected binding gated", selected: "dev", selectedState: "canonical", blocked: true},
		{name: "selected pending gated", selected: "dev", selectedState: "pending", blocked: true},
		{name: "enabled selected binding", selected: "dev", selectedState: "canonical", enabled: true},
		{name: "missing selection", selected: "missing", defaultState: "canonical", missing: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearPreviewProcess(t)
			t.Setenv("AZD_ENVIRONMENT", tt.selected)
			if tt.enabled {
				t.Setenv(synthesis.ACRReuseEnvVar, "true")
			}
			env := &infraEjectEnvironmentServer{
				testEnvironmentServiceServer: testEnvironmentServiceServer{
					current: &azdext.Environment{Name: "prod"},
					environments: map[string]*azdext.Environment{
						"dev": {Name: "dev"}, "prod": {Name: "prod"},
					},
					values: map[string]map[string]string{},
				},
				configByEnvironment: map[string]map[string][]byte{},
			}
			before := map[string]map[string]string{}
			for name, state := range map[string]string{"dev": tt.selectedState, "prod": tt.defaultState} {
				fixture := acrFeatureEnvironment(t, state)
				env.values[name] = fixture.values["test"]
				env.values[name]["target"] = name
				before[name] = maps.Clone(env.values[name])
				config := map[string][]byte{}
				if fixture.binding != nil {
					config[synthesis.ACRBindingConfigKey] = fixture.binding
				}
				if fixture.pending != nil {
					config[synthesis.ACRPendingConfigKey] = fixture.pending
				}
				env.configByEnvironment[name] = config
			}
			client := newTestAzdClient(t, env, nil)
			values, err := readInfraEjectEnvironment(t.Context(), client)
			require.Equal(t, before, env.values)
			require.Empty(t, env.setKeys)
			wantName := tt.selected
			if wantName == "" {
				wantName = "prod"
				require.Equal(t, 1, env.getCurrentCalls)
			} else {
				require.Zero(t, env.getCurrentCalls)
			}
			if tt.missing {
				require.Equal(t, codes.NotFound, status.Code(err))
				require.Nil(t, values)
				require.Zero(t, env.getValuesCalls)
				require.Empty(t, env.configReads)
				return
			}
			require.Equal(t, 1, env.getValuesCalls)
			require.NotEmpty(t, env.configReads)
			var paths []string
			for _, request := range env.configReads {
				require.Equal(t, wantName, request.GetEnvName())
				paths = append(paths, request.GetPath())
			}
			if !tt.enabled {
				require.Contains(t, paths, synthesis.ACRPendingConfigKey)
			}
			if tt.blocked {
				requireACRFeatureDisabled(t, err)
				require.Nil(t, values)
				return
			}
			require.NoError(t, err)
			want := maps.Clone(before[wantName])
			want["AZURE_ENV_NAME"] = wantName
			if binding := env.configByEnvironment[wantName][synthesis.ACRBindingConfigKey]; binding != nil {
				want[synthesis.ACRBindingEnvKey] = string(binding)
			}
			require.Equal(t, want, values)
		})
	}
}

func TestInitInfraEjectUsesSelectedEnvironment(t *testing.T) {
	clearPreviewProcess(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("AZD_EXEC_PROJECT_DIR", root)
	t.Setenv("AZD_CWD", root)
	t.Setenv("AZD_ENVIRONMENT", "dev")
	t.Setenv("ENABLE_AGENT", "true")
	content := "name: test\nservices:\n  project:\n    host: azure.ai.project\n" +
		"  agent:\n    host: azure.ai.agent\n    condition: ${ENABLE_AGENT}\n    kind: hosted\n"
	mustWriteFile(t, filepath.Join(root, "azure.yaml"), content)
	env := &testEnvironmentServiceServer{
		current: &azdext.Environment{Name: "prod"},
		environments: map[string]*azdext.Environment{
			"dev": {Name: "dev"}, "prod": {Name: "prod"},
		},
		values: map[string]map[string]string{
			"dev": {"ENABLE_AGENT": "false"},
			"prod": {
				"ENABLE_AGENT":                         "true",
				"AZD_FOUNDRY_ACR_MODE":                 "reuse-connect",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testInitACRID,
			},
		},
	}
	t.Setenv("AZD_SERVER", newTestAzdServer(t, env, nil))
	_, err := executeRoot(t, "init", "--infra=bicep", "--no-prompt")
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(root, "infra", "main.parameters.json"))
	require.NoError(t, err)
	var document struct {
		Parameters map[string]struct {
			Value any `json:"value"`
		} `json:"parameters"`
	}
	require.NoError(t, json.Unmarshal(raw, &document))
	require.Contains(t, document.Parameters, "includeAcr")
	require.Equal(t, false, document.Parameters["includeAcr"].Value)
	require.FileExists(t, filepath.Join(root, "infra", "main.bicep"))
	require.NoDirExists(t, filepath.Join(root, ".azure"))
	require.Empty(t, env.setKeys)
}
