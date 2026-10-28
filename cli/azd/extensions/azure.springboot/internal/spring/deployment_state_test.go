// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDeploymentSafetyCheckerAllowsOnlyMissingDeployment(t *testing.T) {
	resourceCheckCalls := 0
	checker := &azureDeploymentSafetyChecker{
		getDeployment: func(context.Context) (*azdext.GetDeploymentResponse, error) {
			return nil, status.Error(codes.NotFound, "missing")
		},
		verifyNoOwnedResources: func(context.Context, string, map[string]string) error {
			resourceCheckCalls++
			return nil
		},
	}

	require.NoError(t, checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{}))
	require.Equal(t, 1, resourceCheckCalls)
}

func TestDeploymentSafetyCheckerBlocksMissingDeploymentWhenOwnedResourceExists(t *testing.T) {
	checker := &azureDeploymentSafetyChecker{
		getDeployment: func(context.Context) (*azdext.GetDeploymentResponse, error) {
			return nil, status.Error(codes.NotFound, "missing")
		},
		verifyNoOwnedResources: func(context.Context, string, map[string]string) error {
			return missingExistingDatabasePasswordError()
		},
	}

	err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{})
	require.ErrorContains(t, err, "existing infrastructure")
}

func TestDeploymentSafetyCheckerFailsClosedWhenResourceReadFails(t *testing.T) {
	checker := &azureDeploymentSafetyChecker{
		getDeployment: func(context.Context) (*azdext.GetDeploymentResponse, error) {
			return nil, status.Error(codes.NotFound, "missing")
		},
		verifyNoOwnedResources: func(context.Context, string, map[string]string) error {
			return errors.New("resource read denied")
		},
	}

	err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{})
	require.ErrorContains(t, err, "resource read denied")
}

func TestDeploymentSafetyCheckerBlocksExistingPostgresWithMissingLocalOutputs(t *testing.T) {
	postgresID := "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.DBforPostgreSQL/flexibleServers/pg"
	verified := false
	checker := &azureDeploymentSafetyChecker{
		getDeployment: func(context.Context) (*azdext.GetDeploymentResponse, error) {
			return &azdext.GetDeploymentResponse{Deployment: &azdext.Deployment{
				Id:        "/subscriptions/sub/providers/Microsoft.Resources/deployments/dev-123",
				Tags:      map[string]string{azdEnvironmentTag: "dev"},
				Resources: []string{postgresID},
			}}, nil
		},
		verifyOwnership: func(
			_ context.Context,
			envName string,
			values map[string]string,
			resourceID string,
		) error {
			verified = true
			require.Equal(t, "dev", envName)
			require.Equal(t, "sub", values[azureSubscriptionIDEnv])
			require.Equal(t, postgresID, resourceID)
			return nil
		},
	}

	err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{
		azureSubscriptionIDEnv: "sub",
	})
	require.ErrorContains(t, err, "existing infrastructure")
	require.True(t, verified)
}

func TestDeploymentSafetyCheckerBlocksExistingDeploymentWithoutPostgresOutput(t *testing.T) {
	checker := &azureDeploymentSafetyChecker{
		getDeployment: func(context.Context) (*azdext.GetDeploymentResponse, error) {
			return &azdext.GetDeploymentResponse{Deployment: &azdext.Deployment{
				Id:   "/subscriptions/sub/providers/Microsoft.Resources/deployments/dev-123",
				Tags: map[string]string{azdEnvironmentTag: "dev"},
			}}, nil
		},
	}

	err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{})
	require.ErrorContains(t, err, "existing infrastructure")
}

func TestDeploymentSafetyCheckerFailsClosedOnReadAndOwnershipErrors(t *testing.T) {
	t.Run("deployment read", func(t *testing.T) {
		checker := &azureDeploymentSafetyChecker{
			getDeployment: func(context.Context) (*azdext.GetDeploymentResponse, error) {
				return nil, errors.New("access denied")
			},
		}

		err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{})
		require.ErrorContains(t, err, "could not establish")
		require.NotContains(t, err.Error(), "access denied")
	})

	t.Run("deployment ownership", func(t *testing.T) {
		checker := &azureDeploymentSafetyChecker{
			getDeployment: func(context.Context) (*azdext.GetDeploymentResponse, error) {
				return &azdext.GetDeploymentResponse{Deployment: &azdext.Deployment{
					Id:   "/subscriptions/sub/providers/Microsoft.Resources/deployments/dev-123",
					Tags: map[string]string{azdEnvironmentTag: "other"},
				}}, nil
			},
		}

		err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{})
		require.ErrorContains(t, err, "not owned")
	})

	t.Run("resource ownership", func(t *testing.T) {
		checker := &azureDeploymentSafetyChecker{
			getDeployment: func(context.Context) (*azdext.GetDeploymentResponse, error) {
				return &azdext.GetDeploymentResponse{Deployment: &azdext.Deployment{
					Id:   "/subscriptions/sub/providers/Microsoft.Resources/deployments/dev-123",
					Tags: map[string]string{azdEnvironmentTag: "dev"},
					Resources: []string{
						"/subscriptions/sub/resourceGroups/rg/providers/" +
							"Microsoft.DBforPostgreSQL/flexibleServers/pg",
					},
				}}, nil
			},
			verifyOwnership: func(context.Context, string, map[string]string, string) error {
				return errors.New("ownership denied")
			},
		}

		err := checker.EnsureCanGenerate(t.Context(), "dev", map[string]string{})
		require.ErrorContains(t, err, "ownership denied")
	})
}
