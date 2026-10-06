// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/cmd/nextstep"
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
				require.Contains(t, note, "Set up an evaluation suite")
			} else {
				require.NotContains(t, note, "azd ai eval init")
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
					require.NotContains(t, note, "Set up an evaluation suite")
				} else {
					require.Equal(t, 1, strings.Count(note, "azd ai eval init"))
					require.Contains(t, note, "Set up an evaluation suite")
				}
			})
		}
	}
}
