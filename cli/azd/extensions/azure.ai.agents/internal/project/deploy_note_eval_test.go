// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeployNoteNamesTheSupportedEvalCommand(t *testing.T) {
	t.Parallel()

	p := &AgentServiceTargetProvider{}
	artifacts := p.deployArtifacts(
		"agent", "1.0.0",
		"", "https://ep.services.ai.azure.com",
		ActivityProfile{},
		[]agent_yaml.ProtocolVersionRecord{{Protocol: "responses"}},
	)
	require.NotEmpty(t, artifacts)
	note := artifacts[len(artifacts)-1].Metadata["note"]

	assert.NotContains(t, note, "azd ai agent eval",
		"that surface is deprecated and must not be advertised")
	assert.Contains(t, note, "azd ai eval init",
		"the evaluations extension owns evaluation setup")
	assert.Contains(t, note, "aka.ms/azd-agents-invoke",
		"the invocation link is unchanged")
}

func TestFinalizeDeployGuidance(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name               string
		readme             bool
		evalService        bool
		unavailableProject bool
		protocols          []agent_yaml.ProtocolVersionRecord
	}{
		{
			name:      "prompt agent",
			protocols: []agent_yaml.ProtocolVersionRecord{{Protocol: "responses"}},
		},
		{
			name:      "hosted agent with README",
			readme:    true,
			protocols: []agent_yaml.ProtocolVersionRecord{{Protocol: "invocations"}},
		},
		{
			name:        "existing evaluation service",
			evalService: true,
			protocols:   []agent_yaml.ProtocolVersionRecord{{Protocol: "responses"}},
		},
		{
			name:               "project metadata unavailable",
			unavailableProject: true,
			protocols:          []agent_yaml.ProtocolVersionRecord{{Protocol: "responses"}},
		},
		{
			name: "multiple endpoints",
			protocols: []agent_yaml.ProtocolVersionRecord{
				{Protocol: "responses"}, {Protocol: "invocations"},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			if tt.readme {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "src", "agent"), 0o750))
				require.NoError(t, os.WriteFile(
					filepath.Join(root, "src", "agent", "README.md"), []byte("Sample invocation payload."), 0o600,
				))
			}
			service := &azdext.ServiceConfig{
				Name: "local-agent", Host: "azure.ai.agent", RelativePath: "src/agent",
			}
			project := &azdext.ProjectConfig{
				Path: root,
				Services: map[string]*azdext.ServiceConfig{
					service.Name:  service,
					"other-agent": {Name: "other-agent", Host: "azure.ai.agent"},
				},
			}
			if tt.evalService {
				project.Services["quality"] = &azdext.ServiceConfig{Name: "quality", Host: "azure.ai.eval"}
			}
			if tt.unavailableProject {
				project = nil
			}
			env := &deployNoteEnvServer{stubEnvServer: stubEnvServer{values: map[string]string{}}}
			client := newEnvTestClient(t, env, &stubProjectServer{project: project})
			provider := &AgentServiceTargetProvider{
				azdClient: client,
				env:       &azdext.Environment{Name: "test-env"},
			}

			result, err := provider.finalizeDeploy(
				t.Context(), func(string) {}, service,
				map[string]string{"FOUNDRY_PROJECT_ENDPOINT": "https://project.services.ai.azure.com"},
				&agent_api.AgentVersionObject{Name: "deployed-agent", Version: "7"},
				tt.protocols, "", "", false, ActivityProfile{}, nil,
			)
			require.NoError(t, err)
			require.Len(t, result.Artifacts, len(tt.protocols))

			for i, artifact := range result.Artifacts {
				require.Equal(t, "deployed-agent", artifact.Metadata["agentName"])
				require.Equal(t, "7", artifact.Metadata["agentVersion"])
				require.NotContains(t, artifact.Location, "local-agent")
				if i < len(result.Artifacts)-1 {
					require.Empty(t, artifact.Metadata["note"], "guidance belongs only to the last endpoint")
				}
			}
			note := result.Artifacts[len(result.Artifacts)-1].Metadata["note"]
			require.NotContains(t, note, "azd ai agent eval")
			require.NotContains(t, note, "other-agent")
			require.NotContains(t, note, "deployed-agent", "commands select the local service, not the deployed name")
			if tt.readme {
				require.Contains(t, note, "see src/agent/README.md")
				require.NotContains(t, note, "aka.ms/azd-agents-invoke")
			} else {
				require.Equal(t, 1, strings.Count(note, "azd ai eval init"))
				require.Contains(t, note, "aka.ms/azd-agents-invoke")
			}
			if !tt.unavailableProject {
				require.Contains(t, note, "azd ai agent show local-agent")
				require.Contains(t, note, "azd ai agent invoke local-agent ")
			}
			t.Logf("Post-deploy note:\n%s", note)
		})
	}
}

type deployNoteEnvServer struct {
	stubEnvServer
}

func (s *deployNoteEnvServer) GetCurrent(
	context.Context, *azdext.EmptyRequest,
) (*azdext.EnvironmentResponse, error) {
	return &azdext.EnvironmentResponse{Environment: &azdext.Environment{Name: "test-env"}}, nil
}

func (s *deployNoteEnvServer) GetValue(
	_ context.Context, req *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	return &azdext.KeyValueResponse{Value: s.values[req.Key]}, nil
}
