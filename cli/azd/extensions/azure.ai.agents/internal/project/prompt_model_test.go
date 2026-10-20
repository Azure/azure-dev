// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func promptModelServices(t *testing.T, deploymentNames ...string) (
	*azdext.ServiceConfig, map[string]*azdext.ServiceConfig,
) {
	t.Helper()
	deployments := make([]any, 0, len(deploymentNames))
	for _, name := range deploymentNames {
		deployments = append(deployments, map[string]any{
			"name":  name,
			"model": map[string]any{"name": "gpt-4.1-nano", "format": "OpenAI"},
		})
	}
	props, err := structpb.NewStruct(map[string]any{"deployments": deployments})
	require.NoError(t, err)
	agent := &azdext.ServiceConfig{Name: "agent", Host: "azure.ai.agent", Uses: []string{"ai-project"}}
	return agent, map[string]*azdext.ServiceConfig{
		"agent":      agent,
		"ai-project": {Name: "ai-project", Host: FoundryProjectHost, AdditionalProperties: props},
	}
}

func TestResolvePromptAgentModel(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		declared    []string
		env         map[string]string
		want        string
		wantErrText string
	}{
		{
			name:     "reused deployment falls back to environment default",
			model:    "template-chat",
			declared: []string{"template-chat"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         "[]",
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "existing-41",
		},
		{
			name:     "escaped environment list is decoded",
			model:    "template-chat",
			declared: []string{"template-chat"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         `[{\"name\":\"other\"}]`,
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "existing-41",
		},
		{
			name:     "multiple declared deployments cannot be inferred",
			model:    "template-chat",
			declared: []string{"template-chat", "template-embed"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         "[]",
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			wantErrText: "declares 2 deployments",
		},
		{
			name:        "skipped deployment without default",
			model:       "template-chat",
			declared:    []string{"template-chat"},
			env:         map[string]string{"AI_PROJECT_DEPLOYMENTS": "[]"},
			wantErrText: "was skipped for this environment",
		},
		{
			name:     "deployment created in this environment is unchanged",
			model:    "template-chat",
			declared: []string{"template-chat"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         `[{"name":"template-chat"}]`,
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "template-chat",
		},
		{
			name:     "unset environment list is unchanged",
			model:    "template-chat",
			declared: []string{"template-chat"},
			env:      map[string]string{"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41"},
			want:     "template-chat",
		},
		{
			name:     "undeclared model is unchanged",
			model:    "gpt-4.1-mini",
			declared: []string{"template-chat"},
			env: map[string]string{
				"AI_PROJECT_DEPLOYMENTS":         "[]",
				"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41",
			},
			want: "gpt-4.1-mini",
		},
		{
			name:     "environment reference is expanded",
			model:    "${AZURE_AI_MODEL_DEPLOYMENT_NAME}",
			declared: []string{"template-chat"},
			env:      map[string]string{"AZURE_AI_MODEL_DEPLOYMENT_NAME": "existing-41"},
			want:     "existing-41",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, services := promptModelServices(t, tt.declared...)
			got, err := ResolvePromptAgentModel(tt.model, agent, services, t.TempDir(), tt.env)
			if tt.wantErrText != "" {
				require.ErrorContains(t, err, tt.wantErrText)
				localErr, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				require.Equal(t, exterrors.CodeModelDeploymentNotFound, localErr.Code)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
