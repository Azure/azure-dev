// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestDeployArtifacts_EvaluationGuidanceFollowsServiceManifest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		projectServices  map[string]*azdext.ServiceConfig
		wantEvalGuidance bool
	}{
		{
			name: "manifest without evaluation service",
			projectServices: map[string]*azdext.ServiceConfig{
				"agent": {Name: "agent", Host: "azure.ai.agent"},
			},
			wantEvalGuidance: true,
		},
		{
			name: "manifest with evaluation service",
			projectServices: map[string]*azdext.ServiceConfig{
				"agent":   {Name: "agent", Host: "azure.ai.agent"},
				"quality": {Name: "quality", Host: evaluationServiceHost},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider := &AgentServiceTargetProvider{projectServices: tt.projectServices}
			artifacts := provider.deployArtifacts(
				"agent",
				"1.0.0",
				"",
				"https://project.services.ai.azure.com",
				ActivityProfile{},
				[]agent_yaml.ProtocolVersionRecord{{Protocol: "responses"}},
			)
			require.NotEmpty(t, artifacts)
			note := artifacts[len(artifacts)-1].Metadata["note"]

			require.Contains(t, note, "aka.ms/azd-agents-invoke")
			require.NotContains(t, note, "azd ai agent eval generate")
			if tt.wantEvalGuidance {
				require.Equal(t, 1, strings.Count(note, "azd ai eval init"))
				require.Equal(t, 1, strings.Count(note, "azd extension install azure.ai.evaluations"))
				require.Contains(t, note, "Set up an evaluation suite")
			} else {
				require.NotContains(t, note, "azd ai eval init")
				require.NotContains(t, note, "azd extension install azure.ai.evaluations")
				require.NotContains(t, note, "Set up an evaluation suite")
			}
		})
	}
}

func TestAugmentDeployNote_EvaluationGuidanceSurvivesReadmeReplacement(t *testing.T) {
	t.Parallel()

	for _, readme := range []string{"none", "service", "root"} {
		for _, declared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/eval-declared=%t", readme, declared), func(t *testing.T) {
				t.Parallel()

				projectRoot := t.TempDir()
				relativePath := "src/agent"
				if readme == "root" {
					relativePath = "."
				}
				if readme != "none" {
					servicePath := filepath.Join(projectRoot, relativePath)
					require.NoError(t, os.MkdirAll(servicePath, 0o750))
					require.NoError(t, os.WriteFile(filepath.Join(servicePath, "README.md"), []byte("agent"), 0o600))
				}
				services := map[string]*azdext.ServiceConfig{
					"agent": {Name: "agent", Host: "azure.ai.agent"},
				}
				if declared {
					services["quality"] = &azdext.ServiceConfig{Name: "quality", Host: evaluationServiceHost}
				}
				provider := &AgentServiceTargetProvider{projectServices: services}
				artifacts := provider.deployArtifacts(
					"agent", "1.0.0", "", "https://project.services.ai.azure.com",
					ActivityProfile{}, []agent_yaml.ProtocolVersionRecord{{Protocol: "responses"}},
				)
				require.NotEmpty(t, artifacts)
				state := &nextstep.State{
					Services: []nextstep.ServiceState{{
						Name: "agent", RelativePath: relativePath, Protocol: "responses", IsDeployed: true,
					}},
				}
				augmentDeployNote(state, artifacts, projectRoot, "")

				note := artifacts[len(artifacts)-1].Metadata["note"]
				require.Equal(t, 1, strings.Count(note, "Next:"))
				require.NotContains(t, note, "azd ai agent eval generate")
				if readme == "none" {
					require.Contains(t, note, "aka.ms/azd-agents-invoke")
				} else {
					require.NotContains(t, note, "aka.ms/azd-agents-invoke")
					require.Contains(t, note, "README.md")
				}
				if declared {
					require.NotContains(t, note, "azd ai eval init")
					require.NotContains(t, note, "azd extension install azure.ai.evaluations")
					require.NotContains(t, note, "Set up an evaluation suite")
				} else {
					require.Equal(t, 1, strings.Count(note, "azd ai eval init"))
					require.Equal(t, 1, strings.Count(note, "azd extension install azure.ai.evaluations"))
					require.Contains(t, note, "Set up an evaluation suite")
				}
			})
		}
	}
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
			name:        "existing evaluation service with README",
			readme:      true,
			evalService: true,
			protocols:   []agent_yaml.ProtocolVersionRecord{{Protocol: "invocations"}},
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
				project.Services["quality"] = &azdext.ServiceConfig{Name: "quality", Host: evaluationServiceHost}
			}
			if tt.unavailableProject {
				project = nil
			}
			env := &deployNoteEnvServer{stubEnvServer: stubEnvServer{values: map[string]string{}}}
			client := newEnvTestClient(t, env, &stubProjectServer{project: project})
			provider := &AgentServiceTargetProvider{
				azdClient:       client,
				env:             &azdext.Environment{Name: "test-env"},
				projectServices: project.GetServices(),
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
				require.Contains(t, artifact.Location, "/agents/deployed-agent/")
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
				require.Contains(t, note, "aka.ms/azd-agents-invoke")
			}
			if tt.evalService {
				require.NotContains(t, note, "azd ai eval init")
				require.NotContains(t, note, "azd extension install azure.ai.evaluations")
				require.NotContains(t, note, "Set up an evaluation suite")
			} else {
				require.Equal(t, 1, strings.Count(note, "azd ai eval init"))
				require.Equal(t, 1, strings.Count(note, "azd extension install azure.ai.evaluations"))
			}
			if tt.unavailableProject {
				require.NotContains(t, note, "Next:")
			} else {
				require.Equal(t, 1, strings.Count(note, "Next:"))
				require.Contains(t, note, "azd ai agent show local-agent")
				require.Contains(t, note, "azd ai agent invoke local-agent ")
			}
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
