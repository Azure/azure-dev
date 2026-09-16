// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandDeployment(t *testing.T) {
	t.Parallel()

	got, err := expandDeployment(synthesis.Deployment{
		Name: "${DEPLOYMENT_NAME}",
		Model: synthesis.DeploymentModel{
			Name:    "${MODEL_NAME}",
			Format:  "${MODEL_FORMAT}",
			Version: "${MODEL_VERSION}",
		},
		Sku: synthesis.DeploymentSku{Name: "${SKU_NAME}"},
	}, map[string]string{
		"DEPLOYMENT_NAME": "chat",
		"MODEL_NAME":      "gpt-4.1",
		"MODEL_FORMAT":    "OpenAI",
		"MODEL_VERSION":   "2025-04-14",
		"SKU_NAME":        "GlobalStandard",
	})

	require.NoError(t, err)
	require.Equal(t, synthesis.Deployment{
		Name: "chat",
		Model: synthesis.DeploymentModel{
			Name:    "gpt-4.1",
			Format:  "OpenAI",
			Version: "2025-04-14",
		},
		Sku: synthesis.DeploymentSku{Name: "GlobalStandard"},
	}, got)
}

func TestProjectAddMutationIncludesDeploymentReconciliation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		mutation          string
		reconciledChanged bool
		want              string
	}{
		{
			name:              "deployment update changes unchanged project",
			mutation:          "unchanged",
			reconciledChanged: true,
			want:              "updated",
		},
		{
			name:              "unchanged deployment preserves unchanged project",
			mutation:          "unchanged",
			reconciledChanged: false,
			want:              "unchanged",
		},
		{
			name:              "created project remains created",
			mutation:          "created",
			reconciledChanged: true,
			want:              "created",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, projectAddMutation(
				tt.mutation, tt.reconciledChanged,
			))
		})
	}
}
