// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateProjectReconcileRequest(t *testing.T) {
	request := &projectReconcileRequest{
		SchemaVersion: 1,
		Project: projectReconcileProject{
			ProjectID: " project-id ",
		},
		Deployments: []projectReconcileDeployment{{
			Name: " chat ",
			Model: synthesis.DeploymentModel{
				Format:  " OpenAI ",
				Name:    " gpt-4.1 ",
				Version: " 2025-04-14 ",
			},
			Sku: synthesis.DeploymentSku{
				Name:     " GlobalStandard ",
				Capacity: 10,
			},
			Location: " eastus ",
		}},
		DefaultDeployment: " chat ",
	}

	require.NoError(t, validateProjectReconcileRequest(request))
	assert.Equal(t, "project-id", request.Project.ProjectID)
	assert.Equal(t, "chat", request.Deployments[0].Name)
	assert.Equal(t, "OpenAI", request.Deployments[0].Model.Format)
	assert.Equal(t, "eastus", request.Deployments[0].Location)
	assert.True(t, request.noPrompt())
}

func TestValidateProjectReconcileRequestRejectsInvalidInput(t *testing.T) {
	validDeployment := projectReconcileDeployment{
		Name: "chat",
		Model: synthesis.DeploymentModel{
			Format:  "OpenAI",
			Name:    "gpt-4.1",
			Version: "2025-04-14",
		},
		Sku: synthesis.DeploymentSku{
			Name:     "GlobalStandard",
			Capacity: 1,
		},
	}
	tests := []struct {
		name    string
		request projectReconcileRequest
		code    string
	}{
		{
			name: "schema",
			request: projectReconcileRequest{
				SchemaVersion: 2,
			},
			code: exterrors.CodeInvalidParameter,
		},
		{
			name: "project targets",
			request: projectReconcileRequest{
				SchemaVersion: 1,
				Project: projectReconcileProject{
					ProjectID:       "id",
					ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/p",
				},
			},
			code: exterrors.CodeConflictingArguments,
		},
		{
			name: "infra and deployments",
			request: projectReconcileRequest{
				SchemaVersion: 1,
				Infra:         "bicep",
				Deployments:   []projectReconcileDeployment{validDeployment},
			},
			code: exterrors.CodeConflictingArguments,
		},
		{
			name: "duplicate deployment",
			request: projectReconcileRequest{
				SchemaVersion: 1,
				Deployments: []projectReconcileDeployment{
					validDeployment,
					validDeployment,
				},
			},
			code: "project_deployment_duplicate",
		},
		{
			name: "default deployment",
			request: projectReconcileRequest{
				SchemaVersion:     1,
				Deployments:       []projectReconcileDeployment{validDeployment},
				DefaultDeployment: "other",
			},
			code: "project_deployment_default_invalid",
		},
		{
			name: "location conflict",
			request: projectReconcileRequest{
				SchemaVersion: 1,
				Deployments: []projectReconcileDeployment{
					{
						Name:     validDeployment.Name,
						Model:    validDeployment.Model,
						Sku:      validDeployment.Sku,
						Location: "eastus",
					},
					{
						Name:     "embedding",
						Model:    validDeployment.Model,
						Sku:      validDeployment.Sku,
						Location: "westus",
					},
				},
			},
			code: "project_deployment_location_conflict",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateProjectReconcileRequest(&test.request)
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			assert.Equal(t, test.code, localErr.Code)
		})
	}
}

func TestLoadProjectReconcileRequest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "request.json")
	request := projectReconcileRequest{
		SchemaVersion: 1,
		NoPrompt:      new(false),
	}
	raw, err := json.Marshal(request)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0600))

	loaded, err := loadProjectReconcileRequest(path)
	require.NoError(t, err)
	require.NotNil(t, loaded.NoPrompt)
	assert.False(t, loaded.noPrompt())

	require.NoError(t, os.WriteFile(path, append(raw, []byte("\n{}")...), 0600))
	_, err = loadProjectReconcileRequest(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trailing data")

	require.NoError(t, os.WriteFile(
		path,
		[]byte(`{"schemaVersion":1,"unexpected":true}`),
		0600,
	))
	_, err = loadProjectReconcileRequest(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown field")
}

func TestProjectReconcileEnvironmentSets(t *testing.T) {
	request := &projectReconcileRequest{
		Deployments: []projectReconcileDeployment{{
			Location: "eastus",
		}},
		DefaultDeployment: "chat",
	}
	assert.Equal(t, map[string]string{
		"AZURE_AI_DEPLOYMENTS_LOCATION":  "eastus",
		"AZURE_AI_MODEL_DEPLOYMENT_NAME": "chat",
	}, projectReconcileEnvironmentSets(request))
}

func TestRollbackProjectReconcileRunsInReverseOrder(t *testing.T) {
	var order []int
	err := rollbackProjectReconcile(
		os.ErrInvalid,
		func() error {
			order = append(order, 1)
			return nil
		},
		func() error {
			order = append(order, 2)
			return nil
		},
	)
	require.ErrorIs(t, err, os.ErrInvalid)
	assert.Equal(t, []int{2, 1}, order)
}
