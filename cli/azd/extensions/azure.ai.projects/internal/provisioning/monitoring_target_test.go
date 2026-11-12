// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"maps"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviousMonitoringTarget(t *testing.T) {
	t.Parallel()
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	parameters := map[string]any{
		"location":           map[string]any{"value": "westus3"},
		"resourceGroupName":  map[string]any{"value": "rg"},
		"foundryProjectName": map[string]any{"value": "project"},
		"resourceTokenSalt":  map[string]any{"value": "env"},
	}
	tests := []struct {
		name        string
		change      string
		priorMode   string
		priorID     string
		failed      bool
		wantBinding bool
		wantOwned   bool
		wantLegacy  bool
	}{
		{name: "owned graph remains create", priorMode: "create", wantBinding: true, wantOwned: true},
		{name: "BYO does not gain component ownership", priorMode: "reuse-connect", wantBinding: true},
		{name: "pre-feature deployment is not fresh consent", wantBinding: true, wantLegacy: true},
		{name: "prior opt out is not fresh consent", priorMode: "none", wantBinding: true, wantLegacy: true},
		{name: "changed group", change: "resourceGroupName"},
		{name: "changed project name", change: "foundryProjectName"},
		{name: "changed salt", change: "resourceTokenSalt"},
		{name: "changed location", change: "location"},
		{name: "stale output", priorID: projectID + "-other"},
		{name: "unsuccessful deployment", failed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := maps.Clone(parameters)
			if tt.change != "" {
				old[tt.change] = map[string]any{"value": "other"}
			}
			if tt.priorMode != "" {
				old["monitoringMode"] = map[string]any{"value": tt.priorMode}
			}
			id := projectID
			if tt.priorID != "" {
				id = tt.priorID
			}
			state := armresources.ProvisioningStateSucceeded
			if tt.failed {
				state = armresources.ProvisioningStateFailed
			}
			prior := &armresources.DeploymentPropertiesExtended{
				ProvisioningState: &state,
				Parameters:        old,
				Outputs: map[string]any{
					"AZURE_AI_PROJECT_ID": map[string]any{"type": "String", "value": id},
				},
			}
			binding := previousMonitoringTarget(parameters, prior, "sub")
			if !tt.wantBinding {
				assert.Nil(t, binding)
				return
			}
			require.NotNil(t, binding)
			assert.Equal(t, projectID, binding.projectID)
			assert.Equal(t, tt.wantOwned, binding.owned)
			assert.Equal(t, tt.wantLegacy, binding.legacy)
		})
	}
	assert.Nil(t, previousMonitoringTarget(parameters, nil, "sub"))
}

func TestMonitoringProjectFromWhatIf(t *testing.T) {
	t.Parallel()
	const prefix = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/"
	project := prefix + "account/projects/project"
	tests := []struct {
		name    string
		changes []*armresources.WhatIfChange
		wantErr string
	}{
		{
			name: "actual target and ignored existing sibling",
			changes: []*armresources.WhatIfChange{
				{ResourceID: new(prefix + "sibling/projects/project"), ChangeType: new(armresources.ChangeTypeIgnore)},
				{ResourceID: new(project), ChangeType: new(armresources.ChangeTypeCreate)},
			},
		},
		{
			name: "reprovision no change",
			changes: []*armresources.WhatIfChange{
				{ResourceID: new(project), ChangeType: new(armresources.ChangeTypeNoChange)},
			},
		},
		{name: "nested expansion missing", wantErr: "did not resolve"},
		{
			name: "wrong project cannot authorize writes", wantErr: "did not resolve",
			changes: []*armresources.WhatIfChange{
				{ResourceID: new(project + "-other"), ChangeType: new(armresources.ChangeTypeCreate)},
			},
		},
		{
			name: "ambiguous accounts", wantErr: "multiple",
			changes: []*armresources.WhatIfChange{
				{ResourceID: new(project), ChangeType: new(armresources.ChangeTypeCreate)},
				{ResourceID: new(prefix + "different/projects/project"), ChangeType: new(armresources.ChangeTypeCreate)},
			},
		},
		{
			name: "unsupported expansion", wantErr: "could not resolve",
			changes: []*armresources.WhatIfChange{
				{ResourceID: new(project), ChangeType: new(armresources.ChangeTypeUnsupported)},
			},
		},
		{
			name: "deletion is not a setup target", wantErr: "unsafe project change",
			changes: []*armresources.WhatIfChange{
				{ResourceID: new(project), ChangeType: new(armresources.ChangeTypeDelete)},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := monitoringProjectFromWhatIf(armresources.WhatIfOperationResult{
				Properties: &armresources.WhatIfOperationProperties{Changes: tt.changes},
			}, "sub", "rg", "project")
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, actual)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, project, actual)
		})
	}
}
