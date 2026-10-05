// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"strings"
	"testing"

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
