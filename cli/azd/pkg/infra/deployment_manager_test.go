// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDeploymentService is a minimal stub that satisfies
// azapi.DeploymentService for the subset used by
// DeploymentManager unit tests.
type fakeDeploymentService struct {
	azapi.DeploymentService
	generateName func(string) string
	calcHash     func() (string, error)
}

func (f *fakeDeploymentService) GenerateDeploymentName(
	base string,
) string {
	if f.generateName != nil {
		return f.generateName(base)
	}
	return base + "-generated"
}

func (f *fakeDeploymentService) CalculateTemplateHash(
	_ context.Context,
	_ string,
	_ azure.RawArmTemplate,
) (string, error) {
	if f.calcHash != nil {
		return f.calcHash()
	}
	return "abc123", nil
}

func TestNewDeploymentManager(t *testing.T) {
	dm := NewDeploymentManager(
		&fakeDeploymentService{}, nil, nil,
	)
	require.NotNil(t, dm)
}

func TestGenerateDeploymentName(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		expected string
	}{
		{
			name:     "simple base",
			base:     "myenv",
			expected: "myenv-generated",
		},
		{
			name:     "empty base",
			base:     "",
			expected: "-generated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dm := NewDeploymentManager(
				&fakeDeploymentService{}, nil, nil,
			)
			result := dm.GenerateDeploymentName(tt.base)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateTemplateHash(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dm := NewDeploymentManager(
			&fakeDeploymentService{
				calcHash: func() (string, error) {
					return "hash-xyz", nil
				},
			}, nil, nil,
		)
		hash, err := dm.CalculateTemplateHash(
			context.Background(),
			"sub-1",
			azure.RawArmTemplate("{}"),
		)
		require.NoError(t, err)
		assert.Equal(t, "hash-xyz", hash)
	})

	t.Run("error", func(t *testing.T) {
		dm := NewDeploymentManager(
			&fakeDeploymentService{
				calcHash: func() (string, error) {
					return "", fmt.Errorf("hash failed")
				},
			}, nil, nil,
		)
		_, err := dm.CalculateTemplateHash(
			context.Background(),
			"sub-1",
			azure.RawArmTemplate("{}"),
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "hash failed")
	})
}

func TestSubscriptionScope(t *testing.T) {
	dm := NewDeploymentManager(
		&fakeDeploymentService{}, nil, nil,
	)
	scope := dm.SubscriptionScope("sub-1", "eastus")
	require.NotNil(t, scope)
	assert.Equal(t, "sub-1", scope.SubscriptionId())
	assert.Equal(t, "eastus", scope.Location())
}

func TestResourceGroupScope(t *testing.T) {
	dm := NewDeploymentManager(
		&fakeDeploymentService{}, nil, nil,
	)
	scope := dm.ResourceGroupScope("sub-1", "rg-1")
	require.NotNil(t, scope)
	assert.Equal(t, "sub-1", scope.SubscriptionId())
	assert.Equal(t, "rg-1", scope.ResourceGroupName())
}

func TestSubscriptionDeploymentFactory(t *testing.T) {
	dm := NewDeploymentManager(
		&fakeDeploymentService{}, nil, nil,
	)
	scope := dm.SubscriptionScope("sub-1", "eastus")
	deployment := dm.SubscriptionDeployment(scope, "dep-1")
	require.NotNil(t, deployment)
	assert.Equal(t, "dep-1", deployment.Name())
	assert.Equal(t, "sub-1", deployment.SubscriptionId())
}

func TestResourceGroupDeploymentFactory(t *testing.T) {
	dm := NewDeploymentManager(
		&fakeDeploymentService{}, nil, nil,
	)
	scope := dm.ResourceGroupScope("sub-1", "rg-1")
	deployment := dm.ResourceGroupDeployment(scope, "dep-1")
	require.NotNil(t, deployment)
	assert.Equal(t, "dep-1", deployment.Name())
	assert.Equal(t, "rg-1", deployment.ResourceGroupName())
}

func TestProgressDisplay(t *testing.T) {
	dm := NewDeploymentManager(
		&fakeDeploymentService{}, nil, nil,
	)
	scope := dm.SubscriptionScope("sub-1", "eastus")
	deployment := dm.SubscriptionDeployment(scope, "dep-1")
	display := dm.ProgressDisplay(deployment)
	require.NotNil(t, display)
}

// fakeScope stubs Scope for CompletedDeployments tests.
type fakeScope struct {
	subscriptionId string
	deployments    []*azapi.ResourceDeployment
	err            error
}

func (f *fakeScope) SubscriptionId() string {
	return f.subscriptionId
}

func (f *fakeScope) ListDeployments(
	_ context.Context,
) ([]*azapi.ResourceDeployment, error) {
	return f.deployments, f.err
}

func (f *fakeScope) Deployment(name string) Deployment {
	return nil
}

func TestCompletedDeployments(t *testing.T) {
	now := time.Now().UTC()

	t.Run("matches by env tag", func(t *testing.T) {
		envName := "myenv"
		tagVal := envName
		scope := &fakeScope{
			subscriptionId: "sub-1",
			deployments: []*azapi.ResourceDeployment{
				{
					Name: "deploy-1",
					Tags: map[string]*string{
						azure.TagKeyAzdEnvName: &tagVal,
					},
					ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
					Timestamp:         now,
				},
			},
		}

		dm := NewDeploymentManager(
			&fakeDeploymentService{}, nil, nil,
		)
		result, err := dm.CompletedDeployments(
			context.Background(), scope, envName, "", "",
		)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "deploy-1", result[0].Name)
	})

	t.Run("matches by env and layer tag", func(t *testing.T) {
		envName := "myenv"
		layerName := "layer1"
		envTag := envName
		layerTag := layerName
		scope := &fakeScope{
			subscriptionId: "sub-1",
			deployments: []*azapi.ResourceDeployment{
				{
					Name: "deploy-layer1",
					Tags: map[string]*string{
						azure.TagKeyAzdEnvName:   &envTag,
						azure.TagKeyAzdLayerName: &layerTag,
					},
					ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
					Timestamp:         now,
				},
			},
		}

		dm := NewDeploymentManager(
			&fakeDeploymentService{}, nil, nil,
		)
		result, err := dm.CompletedDeployments(
			context.Background(),
			scope, envName, layerName, "",
		)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, "deploy-layer1", result[0].Name)
	})

	t.Run("legacy match by exact deployment name",
		func(t *testing.T) {
			envName := "myenv"
			scope := &fakeScope{
				subscriptionId: "sub-1",
				deployments: []*azapi.ResourceDeployment{
					{
						Name:              envName,
						Tags:              map[string]*string{},
						ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
						Timestamp:         now,
					},
				},
			}

			dm := NewDeploymentManager(
				&fakeDeploymentService{}, nil, nil,
			)
			result, err := dm.CompletedDeployments(
				context.Background(), scope, envName, "", "",
			)
			require.NoError(t, err)
			require.Len(t, result, 1)
			assert.Equal(t, envName, result[0].Name)
		})

	t.Run("hint fallback returns partial matches",
		func(t *testing.T) {
			scope := &fakeScope{
				subscriptionId: "sub-1",
				deployments: []*azapi.ResourceDeployment{
					{
						Name:              "myenv-deploy-001",
						Tags:              map[string]*string{},
						ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
						Timestamp:         now,
					},
					{
						Name:              "myenv-deploy-002",
						Tags:              map[string]*string{},
						ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
						Timestamp:         now.Add(-time.Hour),
					},
					{
						Name:              "other-deploy",
						Tags:              map[string]*string{},
						ProvisioningState: azapi.DeploymentProvisioningStateSucceeded,
						Timestamp:         now,
					},
				},
			}

			dm := NewDeploymentManager(
				&fakeDeploymentService{}, nil, nil,
			)
			result, err := dm.CompletedDeployments(
				context.Background(),
				scope, "myenv", "", "",
			)
			require.NoError(t, err)
			assert.Len(t, result, 2)
		})

	t.Run("skips non-terminal deployments", func(t *testing.T) {
		scope := &fakeScope{
			subscriptionId: "sub-1",
			deployments: []*azapi.ResourceDeployment{
				{
					Name:              "myenv-running",
					Tags:              map[string]*string{},
					ProvisioningState: azapi.DeploymentProvisioningStateRunning,
					Timestamp:         now,
				},
			},
		}

		dm := NewDeploymentManager(
			&fakeDeploymentService{}, nil, nil,
		)
		_, err := dm.CompletedDeployments(
			context.Background(),
			scope, "myenv-running", "", "",
		)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrDeploymentsNotFound)
	})

	t.Run("no matching deployments returns error",
		func(t *testing.T) {
			scope := &fakeScope{
				subscriptionId: "sub-1",
				deployments:    []*azapi.ResourceDeployment{},
			}

			dm := NewDeploymentManager(
				&fakeDeploymentService{}, nil, nil,
			)
			_, err := dm.CompletedDeployments(
				context.Background(),
				scope, "myenv", "", "",
			)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrDeploymentsNotFound)
		})

	t.Run("list error propagated", func(t *testing.T) {
		scope := &fakeScope{
			subscriptionId: "sub-1",
			err:            fmt.Errorf("list failed"),
		}

		dm := NewDeploymentManager(
			&fakeDeploymentService{}, nil, nil,
		)
		_, err := dm.CompletedDeployments(
			context.Background(),
			scope, "myenv", "", "",
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "list failed")
	})
}
