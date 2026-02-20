// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/stretchr/testify/require"
)

func TestSoftDeleteHint(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantHint bool
		contains string
	}{
		{
			name:     "NonArmError",
			err:      fmt.Errorf("random error"),
			wantHint: false,
		},
		{
			name:     "NilError",
			err:      nil,
			wantHint: false,
		},
		{
			name: "FlagMustBeSetForRestore",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "FlagMustBeSetForRestore",
					Message: "need to restore",
				},
			},
			wantHint: true,
			contains: "azd down --purge",
		},
		{
			name: "ConflictError",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "ConflictError",
					Message: "conflict occurred",
				},
			},
			wantHint: true,
			contains: "soft-deleted resource",
		},
		{
			name: "ConflictWithSoftDeleteMessage",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "Conflict",
					Message: "vault in soft delete state",
				},
			},
			wantHint: true,
			contains: "azd down --purge",
		},
		{
			name: "ConflictWithPurgeMessage",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "Conflict",
					Message: "recover or purge the deleted vault",
				},
			},
			wantHint: true,
			contains: "azd down --purge",
		},
		{
			name: "ConflictWithDeletedVaultMessage",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "Conflict",
					Message: "A deleted vault with the same name exists",
				},
			},
			wantHint: true,
			contains: "azd down --purge",
		},
		{
			name: "ConflictWithoutSoftDeleteMessage",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "Conflict",
					Message: "resource already exists",
				},
			},
			wantHint: false,
		},
		{
			name: "NestedSoftDeleteError",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "DeploymentFailed",
					Message: "deployment failed",
					Inner: []*azapi.DeploymentErrorLine{
						{
							Code:    "ResourceDeploymentFailure",
							Message: "resource failed",
							Inner: []*azapi.DeploymentErrorLine{
								{
									Code:    "FlagMustBeSetForRestore",
									Message: "need to restore",
								},
							},
						},
					},
				},
			},
			wantHint: true,
			contains: "azd down --purge",
		},
		{
			name: "NilDetails",
			err: &azapi.AzureDeploymentError{
				Details: nil,
			},
			wantHint: false,
		},
		{
			name: "RequestConflictWithSoftDelete",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "RequestConflict",
					Message: "soft-delete protection prevents this",
				},
			},
			wantHint: true,
			contains: "azd down --purge",
		},
		{
			name: "RequestConflictWithoutSoftDelete",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "RequestConflict",
					Message: "something else is conflicting",
				},
			},
			wantHint: false,
		},
		{
			name: "CaseInsensitiveSoftDeleteMessage",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "Conflict",
					Message: "Soft Delete state prevents creation",
				},
			},
			wantHint: true,
			contains: "azd down --purge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := softDeleteHint(tt.err)
			if tt.wantHint {
				require.NotEmpty(t, hint,
					"expected a hint for %s", tt.name)
				require.Contains(t, hint, tt.contains,
					"hint should contain %q", tt.contains)
			} else {
				require.Empty(t, hint,
					"expected no hint for %s", tt.name)
			}
		})
	}
}
