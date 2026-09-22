// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

// deployModeProjectServer is a minimal ProjectService stub that
// serves a fixed set of services and records the config writes
// applyDeployModeToService makes.
type deployModeProjectServer struct {
	azdext.UnimplementedProjectServiceServer

	mu       sync.Mutex
	path     string
	services map[string]*azdext.ServiceConfig
	sets     map[string]map[string]any // serviceName -> path -> value
	unsets   map[string][]string       // serviceName -> unset paths
}

func (s *deployModeProjectServer) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &azdext.GetProjectResponse{
		Project: &azdext.ProjectConfig{Path: s.path, Services: s.services},
	}, nil
}

func (s *deployModeProjectServer) SetServiceConfigValue(
	_ context.Context, req *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sets == nil {
		s.sets = map[string]map[string]any{}
	}
	if s.sets[req.ServiceName] == nil {
		s.sets[req.ServiceName] = map[string]any{}
	}
	var v any
	if req.Value != nil {
		v = req.Value.AsInterface()
	}
	s.sets[req.ServiceName][req.Path] = v
	return &azdext.EmptyResponse{}, nil
}

func (s *deployModeProjectServer) UnsetServiceConfig(
	_ context.Context, req *azdext.UnsetServiceConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unsets == nil {
		s.unsets = map[string][]string{}
	}
	s.unsets[req.ServiceName] = append(s.unsets[req.ServiceName], req.Path)
	return &azdext.EmptyResponse{}, nil
}

// agentServiceConfig builds an azure.ai.agent ServiceConfig whose
// additionalProperties carry the supplied deploy-mode block (e.g.
// "docker" or "codeConfiguration"). Pass a nil block for an
// unconfigured service.
func agentServiceConfig(t *testing.T, name string, props map[string]any) *azdext.ServiceConfig {
	t.Helper()
	sc := &azdext.ServiceConfig{Name: name, Host: AiAgentHost}
	if props != nil {
		st, err := structpb.NewStruct(props)
		require.NoError(t, err)
		sc.AdditionalProperties = st
	}
	return sc
}

// TestApplyDeployModeToAdoptedProject verifies that the adopt flow reports
// whether the resolved deploy mode requires an Azure Container Registry.
func TestApplyDeployModeToAdoptedProject(t *testing.T) {
	const svcName = "agent"

	dockerProps := map[string]any{"docker": map[string]any{"remoteBuild": true}}
	codeProps := map[string]any{
		"codeConfiguration": map[string]any{"runtime": "python_3_13", "entryPoint": "app.py"},
	}
	preBuiltService := agentServiceConfig(t, svcName, dockerProps)
	preBuiltService.Image = "registry.example.com/team/agent:v1"
	mixedCodeService := agentServiceConfig(t, svcName, codeProps)
	mixedCodeService.Image = "registry.example.com/team/agent:v1"
	legacyImageProps := map[string]any{
		"kind":  "hosted",
		"name":  svcName,
		"image": "registry.example.com/team/legacy-agent:v1",
	}
	legacyCodeImageProps := map[string]any{
		"kind":              "hosted",
		"name":              svcName,
		"image":             "registry.example.com/team/legacy-agent:v1",
		"codeConfiguration": map[string]any{"runtime": "python_3_13", "entryPoint": "app.py"},
	}

	tests := []struct {
		name          string
		flags         *initFlags
		service       *azdext.ServiceConfig
		wantNeedsACR  bool
		wantLanguage  any
		wantDocker    map[string]any
		wantCodeSet   bool
		wantCodeUnset bool
		wantRegistry  string
		wantImage     string
		wantErr       string
	}{
		{
			name:         "explicit container flag wires ACR",
			flags:        &initFlags{deployMode: "container"},
			service:      agentServiceConfig(t, svcName, nil),
			wantNeedsACR: true,
			wantLanguage: "docker",
			wantDocker:   map[string]any{"remoteBuild": true},
		},
		{
			name:         "explicit code flag skips ACR",
			flags:        &initFlags{deployMode: "code", runtime: "python_3_13", entryPoint: "app.py"},
			service:      agentServiceConfig(t, svcName, nil),
			wantNeedsACR: false,
			wantLanguage: "python",
			wantCodeSet:  true,
		},
		{
			name:         "prebuilt image uses passthrough",
			flags:        &initFlags{image: "myacr.azurecr.io/agent:v1"},
			service:      agentServiceConfig(t, svcName, nil),
			wantNeedsACR: false,
			wantLanguage: "docker",
			wantDocker:   map[string]any{"imagePassthrough": true},
		},
		{
			name: "prebuilt image applies registry connection",
			flags: &initFlags{
				image:              "registry.example.com/team/agent:v1",
				registryConnection: "private-registry",
			},
			service:      agentServiceConfig(t, svcName, nil),
			wantNeedsACR: false,
			wantLanguage: "docker",
			wantDocker:   map[string]any{"imagePassthrough": true},
			wantRegistry: "private-registry",
		},
		{
			name:         "existing image uses passthrough",
			flags:        &initFlags{},
			service:      preBuiltService,
			wantNeedsACR: false,
			wantLanguage: "docker",
			wantDocker:   map[string]any{"imagePassthrough": true},
		},
		{
			name:         "resolved legacy image is promoted and uses passthrough",
			flags:        &initFlags{},
			service:      agentServiceConfig(t, svcName, legacyImageProps),
			wantNeedsACR: false,
			wantLanguage: "docker",
			wantDocker:   map[string]any{"imagePassthrough": true},
			wantImage:    "registry.example.com/team/legacy-agent:v1",
		},
		{
			name: "resolved legacy image applies registry connection",
			flags: &initFlags{
				registryConnection: "private-registry",
			},
			service:      agentServiceConfig(t, svcName, legacyImageProps),
			wantNeedsACR: false,
			wantLanguage: "docker",
			wantDocker:   map[string]any{"imagePassthrough": true},
			wantRegistry: "private-registry",
			wantImage:    "registry.example.com/team/legacy-agent:v1",
		},
		{
			name:  "unqualified resolved legacy image is rejected",
			flags: &initFlags{},
			service: agentServiceConfig(t, svcName, map[string]any{
				"kind": "hosted", "name": svcName, "image": "agent:v1",
			}),
			wantErr: "must be in format registry/image[:tag]",
		},
		{
			name:         "respects sample docker config",
			flags:        &initFlags{},
			service:      agentServiceConfig(t, svcName, dockerProps),
			wantNeedsACR: true,
		},
		{
			name:         "respects sample code config",
			flags:        &initFlags{},
			service:      agentServiceConfig(t, svcName, codeProps),
			wantNeedsACR: false,
		},
		{
			name:         "resolved code config takes precedence over resolved legacy image",
			flags:        &initFlags{},
			service:      agentServiceConfig(t, svcName, legacyCodeImageProps),
			wantNeedsACR: false,
		},
		{
			name:         "code config takes precedence over existing image",
			flags:        &initFlags{},
			service:      mixedCodeService,
			wantNeedsACR: false,
		},
		{
			name: "explicit container mode overrides code config for registry image",
			flags: &initFlags{
				deployMode:         "container",
				registryConnection: "private-registry",
			},
			service:       mixedCodeService,
			wantNeedsACR:  false,
			wantLanguage:  "docker",
			wantDocker:    map[string]any{"imagePassthrough": true},
			wantCodeUnset: true,
			wantRegistry:  "private-registry",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := &deployModeProjectServer{
				path:     t.TempDir(),
				services: map[string]*azdext.ServiceConfig{svcName: tc.service},
			}
			client := newProjectRecorderClient(t, server)

			needsACR, err := applyDeployModeToAdoptedProject(t.Context(), tc.flags, client)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				assert.Empty(t, server.sets[svcName])
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantNeedsACR, needsACR)

			sets := server.sets[svcName]
			if tc.wantLanguage != nil {
				assert.Equal(t, tc.wantLanguage, sets["language"])
			}
			if tc.wantDocker != nil {
				assert.Equal(t, tc.wantDocker, sets["docker"])
			}
			if tc.wantCodeSet {
				assert.Contains(t, sets, "codeConfiguration")
			}
			if tc.wantCodeUnset {
				assert.Contains(t, server.unsets[svcName], "codeConfiguration")
			}
			if tc.wantRegistry != "" {
				assert.Equal(t, tc.wantRegistry, sets["registryConnectionId"])
			}
			if tc.wantImage != "" {
				assert.Equal(t, tc.wantImage, sets["image"])
			}
			// A respected sample config must not be rewritten.
			if tc.wantDocker == nil && !tc.wantCodeSet && tc.wantLanguage == nil &&
				tc.wantRegistry == "" && tc.wantImage == "" {
				assert.Empty(t, sets)
			}
		})
	}
}

func TestApplyDeployModeToAdoptedProject_ValidatesRegistryConnection(t *testing.T) {
	const svcName = "agent"

	tests := []struct {
		name        string
		service     *azdext.ServiceConfig
		wantContain string
	}{
		{
			name:        "requires image",
			service:     agentServiceConfig(t, svcName, nil),
			wantContain: "requires a pre-built image",
		},
		{
			name: "rejects code deploy",
			service: agentServiceConfig(t, svcName, map[string]any{
				"codeConfiguration": map[string]any{"runtime": "python_3_13", "entryPoint": "app.py"},
			}),
			wantContain: "cannot be used with code deploy",
		},
		{
			name: "rejects unqualified resolved legacy image before writing connection",
			service: agentServiceConfig(t, svcName, map[string]any{
				"kind": "hosted", "name": svcName, "image": "agent:v1",
			}),
			wantContain: "must be in format registry/image[:tag]",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &deployModeProjectServer{
				path:     t.TempDir(),
				services: map[string]*azdext.ServiceConfig{svcName: test.service},
			}
			client := newProjectRecorderClient(t, server)

			_, err := applyDeployModeToAdoptedProject(t.Context(), &initFlags{
				registryConnection: "private-registry",
			}, client)
			require.ErrorContains(t, err, test.wantContain)
			assert.Empty(t, server.sets[svcName])
		})
	}
}

// TestApplyDeployModeToAdoptedProject_NoAgentServices verifies that
// a project without any azure.ai.agent service reports no container
// deploy (so ACR is skipped) rather than erroring.
func TestApplyDeployModeToAdoptedProject_NoAgentServices(t *testing.T) {
	server := &deployModeProjectServer{
		path: t.TempDir(),
		services: map[string]*azdext.ServiceConfig{
			"web": {Name: "web", Host: "containerapp"},
		},
	}
	client := newProjectRecorderClient(t, server)

	usesContainer, err := applyDeployModeToAdoptedProject(t.Context(), &initFlags{}, client)
	require.NoError(t, err)
	assert.False(t, usesContainer)
}

func TestApplyDeployModeToAdoptedProject_SkipsPromptAgents(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, projectDir string) *azdext.ServiceConfig
	}{
		{
			name: "direct",
			setup: func(t *testing.T, _ string) *azdext.ServiceConfig {
				t.Helper()
				return agentServiceConfig(t, "agent", map[string]any{
					"kind": "prompt",
					"name": "prompt-agent",
				})
			},
		},
		{
			name: "root ref",
			setup: func(t *testing.T, projectDir string) *azdext.ServiceConfig {
				t.Helper()
				require.NoError(t, os.WriteFile(
					filepath.Join(projectDir, "prompt.yaml"),
					[]byte("kind: prompt\nname: prompt-agent\n"),
					0o600,
				))
				return agentServiceConfig(t, "agent", map[string]any{
					"$ref": "./prompt.yaml",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			server := &deployModeProjectServer{
				path: projectDir,
				services: map[string]*azdext.ServiceConfig{
					"agent": tt.setup(t, projectDir),
				},
			}
			client := newProjectRecorderClient(t, server)

			usesContainer, err := applyDeployModeToAdoptedProject(
				t.Context(),
				&initFlags{deployMode: "container"},
				client,
			)
			require.NoError(t, err)
			assert.False(t, usesContainer)
			assert.Empty(t, server.sets["agent"])
			assert.Empty(t, server.unsets["agent"])
		})
	}
}

func TestApplyDeployModeToAdoptedProject_ResolvesServicePath(t *testing.T) {
	projectDir := t.TempDir()
	serviceDir := filepath.Join(projectDir, "src", "agent")
	require.NoError(t, os.MkdirAll(serviceDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(serviceDir, "pyproject.toml"),
		nil,
		0600,
	))

	service := agentServiceConfig(t, "agent", nil)
	service.RelativePath = filepath.Join("src", "agent")
	server := &deployModeProjectServer{
		path:     projectDir,
		services: map[string]*azdext.ServiceConfig{"agent": service},
	}
	client := newProjectRecorderClient(t, server)

	usesContainer, err := applyDeployModeToAdoptedProject(
		t.Context(),
		&initFlags{noPrompt: true},
		client,
	)
	require.NoError(t, err)
	assert.False(t, usesContainer)
	assert.Equal(t, "python", server.sets["agent"]["language"])
	assert.Contains(t, server.sets["agent"], "codeConfiguration")
}

func TestApplyDeployModeToAdoptedProject_LegacyDefinitions(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(t *testing.T, projectDir string) *azdext.ServiceConfig
		wantErr       bool
		wantContainer bool
	}{
		{
			name:    "config nested",
			wantErr: true,
			setup: func(t *testing.T, _ string) *azdext.ServiceConfig {
				t.Helper()
				config, err := structpb.NewStruct(map[string]any{
					"kind": "hosted",
					"name": "legacy-agent",
					"codeConfiguration": map[string]any{
						"runtime":    "python_3_13",
						"entryPoint": "app.py",
					},
				})
				require.NoError(t, err)
				return &azdext.ServiceConfig{
					Name:   "agent",
					Host:   AiAgentHost,
					Config: config,
				}
			},
		},
		{
			name:          "implicit disk",
			wantContainer: true,
			setup: func(t *testing.T, projectDir string) *azdext.ServiceConfig {
				t.Helper()
				require.NoError(t, os.WriteFile(
					filepath.Join(projectDir, "agent.yaml"),
					[]byte("kind: hosted\nname: legacy-agent\n"+
						"code_configuration:\n  runtime: python_3_13\n  entry_point: app.py\n"),
					0o600,
				))
				return &azdext.ServiceConfig{
					Name:         "agent",
					Host:         AiAgentHost,
					RelativePath: ".",
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir := t.TempDir()
			server := &deployModeProjectServer{
				path: projectDir,
				services: map[string]*azdext.ServiceConfig{
					"agent": tt.setup(t, projectDir),
				},
			}
			client := newProjectRecorderClient(t, server)

			usesContainer, err := applyDeployModeToAdoptedProject(
				t.Context(),
				&initFlags{},
				client,
			)
			if tt.wantErr {
				require.Error(t, err)
				assert.Empty(t, server.sets["agent"])
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantContainer, usesContainer)
			assert.Equal(t, map[string]any{"remoteBuild": true}, server.sets["agent"]["docker"],
				"the implicit disk definition must not influence deploy-mode selection")
		})
	}
}
