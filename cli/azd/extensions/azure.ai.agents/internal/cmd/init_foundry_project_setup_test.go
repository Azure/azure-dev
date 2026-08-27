// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestValidateAcrConnectionInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		acrConnection     string
		skipACR           bool
		createsNewProject bool
		wantMessage       string
	}{
		{name: "empty connection is allowed", skipACR: true},
		{name: "existing container project is allowed", acrConnection: "registry-connection"},
		{
			name:          "code deployment is rejected",
			acrConnection: "registry-connection",
			skipACR:       true,
			wantMessage:   "skips Azure Container Registry configuration",
		},
		{
			name:              "new project is rejected",
			acrConnection:     "registry-connection",
			createsNewProject: true,
			wantMessage:       "requires an existing Foundry project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateAcrConnectionInput(tt.acrConnection, tt.skipACR, tt.createsNewProject)
			if tt.wantMessage == "" {
				require.NoError(t, err)
				return
			}

			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
			require.Contains(t, localErr.Message, tt.wantMessage)
		})
	}
}

func TestConfigureFoundryProjectRejectsAcrConnectionForHeadlessNewProject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		azureContext *azdext.AzureContext
	}{
		{
			name:         "deferred Azure context",
			azureContext: &azdext.AzureContext{Scope: &azdext.AzureScope{}},
		},
		{
			name: "resolved Azure context",
			azureContext: &azdext.AzureContext{Scope: &azdext.AzureScope{
				SubscriptionId: "subscription-id",
				Location:       "eastus2",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := configureFoundryProject(
				t.Context(),
				nil,
				tt.azureContext,
				"test-env",
				"",
				"registry-connection",
				true,
				false,
				false,
			)
			require.Error(t, err)
			require.Contains(t, err.Error(), "requires an existing Foundry project")
		})
	}
}
