// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/stretchr/testify/require"
)

func TestLocalPreflightValidate_ValidTemplate(t *testing.T) {
	template := armTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Resources: armTemplateResources{
			{Type: "Microsoft.Resources/resourceGroups", APIVersion: "2021-04-01", Name: "rg-test"},
		},
	}
	raw, err := json.Marshal(template)
	require.NoError(t, err)

	preflight := newLocalArmPreflight()
	props, err := preflight.validate(azure.RawArmTemplate(raw), nil)

	require.NoError(t, err)
	require.False(t, props.HasRoleAssignments)
}

func TestLocalPreflightValidate_DetectsRoleAssignments(t *testing.T) {
	template := armTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Resources: armTemplateResources{
			{Type: "Microsoft.Resources/resourceGroups", APIVersion: "2021-04-01", Name: "rg-test"},
			{Type: "Microsoft.Authorization/roleAssignments", APIVersion: "2022-04-01", Name: "ra-test"},
		},
	}
	raw, err := json.Marshal(template)
	require.NoError(t, err)

	preflight := newLocalArmPreflight()
	props, err := preflight.validate(azure.RawArmTemplate(raw), nil)

	require.NoError(t, err)
	require.True(t, props.HasRoleAssignments)
}

func TestLocalPreflightValidate_DetectsRoleAssignmentsInNestedDeployment(t *testing.T) {
	innerTemplate := armTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Resources: armTemplateResources{
			{Type: "Microsoft.Authorization/roleAssignments", APIVersion: "2022-04-01", Name: "nested-ra"},
		},
	}
	innerRaw, err := json.Marshal(innerTemplate)
	require.NoError(t, err)

	deploymentProps := map[string]any{
		"template": json.RawMessage(innerRaw),
		"mode":     "Incremental",
	}
	propsRaw, err := json.Marshal(deploymentProps)
	require.NoError(t, err)

	template := armTemplate{
		Schema:         "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		ContentVersion: "1.0.0.0",
		Resources: armTemplateResources{
			{
				Type:       "Microsoft.Resources/deployments",
				APIVersion: "2021-04-01",
				Name:       "nestedDeployment",
				Properties: propsRaw,
			},
		},
	}
	raw, err := json.Marshal(template)
	require.NoError(t, err)

	preflight := newLocalArmPreflight()
	props, err := preflight.validate(azure.RawArmTemplate(raw), nil)

	require.NoError(t, err)
	require.True(t, props.HasRoleAssignments)
}

func TestLocalPreflightValidate_InvalidTemplate(t *testing.T) {
	preflight := newLocalArmPreflight()
	_, err := preflight.validate(azure.RawArmTemplate([]byte(`{}`)), nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "missing required")
}

func TestAnalyzeResources(t *testing.T) {
	tests := []struct {
		name               string
		resources          []preflightResource
		hasRoleAssignments bool
	}{
		{
			name:               "empty resources",
			resources:          nil,
			hasRoleAssignments: false,
		},
		{
			name: "no role assignments",
			resources: []preflightResource{
				{Type: "Microsoft.Storage/storageAccounts"},
				{Type: "Microsoft.Web/sites"},
			},
			hasRoleAssignments: false,
		},
		{
			name: "has role assignments",
			resources: []preflightResource{
				{Type: "Microsoft.Storage/storageAccounts"},
				{Type: "Microsoft.Authorization/roleAssignments"},
			},
			hasRoleAssignments: true,
		},
		{
			name: "case insensitive match",
			resources: []preflightResource{
				{Type: "microsoft.authorization/roleassignments"},
			},
			hasRoleAssignments: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			props := analyzeResources(tt.resources)
			require.Equal(t, tt.hasRoleAssignments, props.HasRoleAssignments)
		})
	}
}
