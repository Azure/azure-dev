// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azurecleanup

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSubscriptionDeploymentsClient struct {
	deployments []subscriptionDeployment
	listErr     error
	deleteErrs  map[string]error
	deleted     []string
}

func (c *fakeSubscriptionDeploymentsClient) list(context.Context) ([]subscriptionDeployment, error) {
	return c.deployments, c.listErr
}

func (c *fakeSubscriptionDeploymentsClient) delete(_ context.Context, name string) error {
	c.deleted = append(c.deleted, name)
	return c.deleteErrs[name]
}

func TestCleanupSubscriptionDeployments_DeletesMatchingEnvironment(t *testing.T) {
	client := &fakeSubscriptionDeploymentsClient{
		deployments: []subscriptionDeployment{
			{name: "matching", tags: map[string]*string{"azd-env-name": new("test-env")}},
			{name: "case-insensitive-key", tags: map[string]*string{"AZD-ENV-NAME": new("test-env")}},
			{name: "other-environment", tags: map[string]*string{"azd-env-name": new("other-env")}},
			{name: "missing-tag"},
			{name: "", tags: map[string]*string{"azd-env-name": new("test-env")}},
		},
	}

	err := cleanupSubscriptionDeployments(t.Context(), client, "test-env")

	require.NoError(t, err)
	assert.Equal(t, []string{"matching", "case-insensitive-key"}, client.deleted)
}

func TestCleanupSubscriptionDeployments_IgnoresMissingDeployment(t *testing.T) {
	client := &fakeSubscriptionDeploymentsClient{
		deployments: []subscriptionDeployment{
			{name: "already-deleted", tags: map[string]*string{"azd-env-name": new("test-env")}},
		},
		deleteErrs: map[string]error{
			"already-deleted": &azcore.ResponseError{StatusCode: 404},
		},
	}

	err := cleanupSubscriptionDeployments(t.Context(), client, "test-env")

	require.NoError(t, err)
	assert.Equal(t, []string{"already-deleted"}, client.deleted)
}

func TestCleanupSubscriptionDeployments_ReportsListFailure(t *testing.T) {
	client := &fakeSubscriptionDeploymentsClient{listErr: errors.New("list failed")}

	err := cleanupSubscriptionDeployments(t.Context(), client, "test-env")

	require.ErrorContains(t, err, "list subscription deployments")
}

func TestCleanupSubscriptionDeployments_ReportsAllDeleteFailures(t *testing.T) {
	client := &fakeSubscriptionDeploymentsClient{
		deployments: []subscriptionDeployment{
			{name: "first", tags: map[string]*string{"azd-env-name": new("test-env")}},
			{name: "second", tags: map[string]*string{"azd-env-name": new("test-env")}},
		},
		deleteErrs: map[string]error{
			"first":  errors.New("first failed"),
			"second": errors.New("second failed"),
		},
	}

	err := cleanupSubscriptionDeployments(t.Context(), client, "test-env")

	require.Error(t, err)
	assert.ErrorContains(t, err, `delete subscription deployment "first"`)
	assert.ErrorContains(t, err, `delete subscription deployment "second"`)
	assert.Equal(t, []string{"first", "second"}, client.deleted)
}
