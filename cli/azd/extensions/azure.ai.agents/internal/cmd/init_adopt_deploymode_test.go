// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
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
	services map[string]*azdext.ServiceConfig
	sets     map[string]map[string]any // serviceName -> path -> value
	unsets   map[string][]string       // serviceName -> unset paths
}

func (s *deployModeProjectServer) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &azdext.GetProjectResponse{Project: &azdext.ProjectConfig{Services: s.services}}, nil
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

// TestApplyDeployModeToAdoptedProject verifies that the adopt flow
// reports whether the resolved deploy mode is a container (Docker)
// deploy so the caller can wire an Azure Container Registry. A
// container agent that never reported itself would leave
// AZURE_CONTAINER_REGISTRY_ENDPOINT unset and fail deploy.
func TestApplyDeployModeToAdoptedProject(t *testing.T) {
	const svcName = "agent"

	dockerProps := map[string]any{"docker": map[string]any{"remoteBuild": true}}
	codeProps := map[string]any{
		"codeConfiguration": map[string]any{"runtime": "python_3_13", "entryPoint": "app.py"},
	}

	tests := []struct {
		name          string
		flags         *initFlags
		service       *azdext.ServiceConfig
		wantContainer bool
		wantLanguage  any
		wantDockerSet bool
		wantCodeSet   bool
	}{
		{
			name:          "explicit container flag wires ACR",
			flags:         &initFlags{deployMode: "container"},
			service:       agentServiceConfig(t, svcName, nil),
			wantContainer: true,
			wantLanguage:  "docker",
			wantDockerSet: true,
		},
		{
			name:          "explicit code flag skips ACR",
			flags:         &initFlags{deployMode: "code", runtime: "python_3_13", entryPoint: "app.py"},
			service:       agentServiceConfig(t, svcName, nil),
			wantContainer: false,
			wantLanguage:  "python",
			wantCodeSet:   true,
		},
		{
			name:          "prebuilt image is container deploy",
			flags:         &initFlags{image: "myacr.azurecr.io/agent:v1"},
			service:       agentServiceConfig(t, svcName, nil),
			wantContainer: true,
			wantLanguage:  "docker",
			wantDockerSet: true,
		},
		{
			name:          "respects sample docker config",
			flags:         &initFlags{},
			service:       agentServiceConfig(t, svcName, dockerProps),
			wantContainer: true,
		},
		{
			name:          "respects sample code config",
			flags:         &initFlags{},
			service:       agentServiceConfig(t, svcName, codeProps),
			wantContainer: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := &deployModeProjectServer{
				services: map[string]*azdext.ServiceConfig{svcName: tc.service},
			}
			client := newProjectRecorderClient(t, server)

			usesContainer, err := applyDeployModeToAdoptedProject(t.Context(), tc.flags, client)
			require.NoError(t, err)
			assert.Equal(t, tc.wantContainer, usesContainer)

			sets := server.sets[svcName]
			if tc.wantLanguage != nil {
				assert.Equal(t, tc.wantLanguage, sets["language"])
			}
			if tc.wantDockerSet {
				assert.Contains(t, sets, "docker")
			}
			if tc.wantCodeSet {
				assert.Contains(t, sets, "codeConfiguration")
			}
			// A respected sample config must not be rewritten.
			if !tc.wantDockerSet && !tc.wantCodeSet && tc.wantLanguage == nil {
				assert.Empty(t, sets)
			}
		})
	}
}

// TestApplyDeployModeToAdoptedProject_NoAgentServices verifies that
// a project without any azure.ai.agent service reports no container
// deploy (so ACR is skipped) rather than erroring.
func TestApplyDeployModeToAdoptedProject_NoAgentServices(t *testing.T) {
	server := &deployModeProjectServer{
		services: map[string]*azdext.ServiceConfig{
			"web": {Name: "web", Host: "containerapp"},
		},
	}
	client := newProjectRecorderClient(t, server)

	usesContainer, err := applyDeployModeToAdoptedProject(t.Context(), &initFlags{}, client)
	require.NoError(t, err)
	assert.False(t, usesContainer)
}
