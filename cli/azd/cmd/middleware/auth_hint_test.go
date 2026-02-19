// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/stretchr/testify/require"
)

func Test_deploymentAuthHint(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantHint string
	}{
		{
			name:     "NonArmError",
			err:      errors.New("some error"),
			wantHint: "",
		},
		{
			name: "AuthorizationFailed",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code: "",
					Inner: []*azapi.DeploymentErrorLine{
						{
							Code:    "AuthorizationFailed",
							Message: "The client does not have authorization",
						},
					},
				},
			},
			wantHint: "You do not have sufficient permissions for this deployment. " +
				"Ensure you have the Owner or Contributor role on the target subscription or resource group.",
		},
		{
			name: "NestedAuthorizationFailed",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code: "",
					Inner: []*azapi.DeploymentErrorLine{
						{
							Code: "",
							Inner: []*azapi.DeploymentErrorLine{
								{
									Code:    "AuthorizationFailed",
									Message: "Authorization failed",
								},
							},
						},
					},
				},
			},
			wantHint: "You do not have sufficient permissions for this deployment. " +
				"Ensure you have the Owner or Contributor role on the target subscription or resource group.",
		},
		{
			name: "NoRegisteredProvider",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "NoRegisteredProviderFound",
					Message: "No registered provider found for location",
				},
			},
			wantHint: "A required Azure resource provider is not registered in your subscription. " +
				"Register it with 'az provider register --namespace <ProviderNamespace>' and retry.",
		},
		{
			name: "RoleAssignmentExists",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "RoleAssignmentExists",
					Message: "The role assignment already exists",
				},
			},
			wantHint: "A role assignment already exists with the same principal and role. " +
				"This is usually safe to ignore. The template should handle this gracefully.",
		},
		{
			name: "PrincipalNotFound",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "PrincipalNotFound",
					Message: "Principal not found in the directory",
				},
			},
			wantHint: "The service principal referenced in the template was not found. " +
				"Ensure the principal exists in your Microsoft Entra ID (formerly Azure Active Directory) tenant " +
				"and has not been recently deleted.",
		},
		{
			name: "ForbiddenWithRoleAssignment",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "Forbidden",
					Message: "Cannot create roleAssignment: insufficient privileges",
				},
			},
			wantHint: "You do not have permission to manage role assignments. " +
				"Ensure you have the Owner or User Access Administrator role on the subscription.",
		},
		{
			name: "UnrelatedArmError",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "ResourceNotFound",
					Message: "Resource was not found",
				},
			},
			wantHint: "",
		},
		{
			name: "NilDetails",
			err: &azapi.AzureDeploymentError{
				Details: nil,
			},
			wantHint: "",
		},
		{
			name: "LinkedAuthorizationFailed",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "LinkedAuthorizationFailed",
					Message: "The linked authorization check failed",
				},
			},
			wantHint: "The deployment requires cross-resource authorization. " +
				"Ensure you have Owner or User Access Administrator role to create role assignments.",
		},
		{
			name: "RoleAssignmentUpdateNotPermitted",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "RoleAssignmentUpdateNotPermitted",
					Message: "Role assignment update not permitted",
				},
			},
			wantHint: "You cannot update the existing role assignment. " +
				"Ensure you have the Owner or User Access Administrator role on the subscription.",
		},
		{
			name: "ForbiddenWithoutRoleAssignment",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "Forbidden",
					Message: "Some other forbidden error",
				},
			},
			wantHint: "",
		},
		{
			name: "ForbiddenWithRoleAssignmentUpperCase",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "Forbidden",
					Message: "Cannot create RoleAssignment for principal",
				},
			},
			wantHint: "You do not have permission to manage role assignments. " +
				"Ensure you have the Owner or User Access Administrator role on the subscription.",
		},
		{
			name: "ForbiddenWithRoleAssignmentSpaced",
			err: &azapi.AzureDeploymentError{
				Details: &azapi.DeploymentErrorLine{
					Code:    "Forbidden",
					Message: "Cannot create role assignment for principal",
				},
			},
			wantHint: "You do not have permission to manage role assignments. " +
				"Ensure you have the Owner or User Access Administrator role on the subscription.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deploymentAuthHint(tt.err)
			require.Equal(t, tt.wantHint, got)
		})
	}
}
