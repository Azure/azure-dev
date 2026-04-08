// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStdDeployments(mockCtx *mocks.MockContext) *StandardDeployments {
	rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
	return NewStandardDeployments(
		mockCtx.SubscriptionCredentialProvider,
		mockCtx.ArmClientOptions,
		rs,
		cloud.AzurePublic(),
		mockCtx.Clock,
	)
}

func makeDeploymentExtended(name string, state armresources.ProvisioningState) armresources.DeploymentExtended {
	now := time.Now()
	return armresources.DeploymentExtended{
		ID:       new("/subscriptions/SUB/providers/Microsoft.Resources/deployments/" + name),
		Name:     new(name),
		Type:     new("Microsoft.Resources/deployments"),
		Location: new("eastus"),
		Tags:     map[string]*string{"env": new("test")},
		Properties: &armresources.DeploymentPropertiesExtended{
			ProvisioningState: new(state),
			Timestamp:         &now,
			TemplateHash:      new("hash123"),
			Outputs:           nil,
			OutputResources:   []*armresources.ResourceReference{},
			Dependencies:      []*armresources.Dependency{},
		},
	}
}

func Test_StdDeployments_CalculateTemplateHash_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "calculateTemplateHash")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armresources.TemplateHashResult{
				TemplateHash: new("abc123hash"),
			})
	})

	hash, err := sd.CalculateTemplateHash(*mockCtx.Context, "SUB",
		azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`)))
	require.NoError(t, err)
	assert.Equal(t, "abc123hash", hash)
}

func Test_StdDeployments_ListSubscriptionDeployments_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/providers/Microsoft.Resources/deployments")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("deploy1", armresources.ProvisioningStateSucceeded)
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armresources.DeploymentListResult{Value: []*armresources.DeploymentExtended{&dep}})
	})

	deployments, err := sd.ListSubscriptionDeployments(*mockCtx.Context, "SUB")
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "deploy1", deployments[0].Name)
	assert.Equal(t, DeploymentProvisioningStateSucceeded, deployments[0].ProvisioningState)
}

func Test_StdDeployments_GetSubscriptionDeployment_Coverage3(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		sd := newStdDeployments(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.Contains(req.URL.Path, "/deployments/deploy1")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			dep := makeDeploymentExtended("deploy1", armresources.ProvisioningStateSucceeded)
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK, dep)
		})

		d, err := sd.GetSubscriptionDeployment(*mockCtx.Context, "SUB", "deploy1")
		require.NoError(t, err)
		assert.Equal(t, "deploy1", d.Name)
		assert.Equal(t, DeploymentProvisioningStateSucceeded, d.ProvisioningState)
		assert.NotEmpty(t, d.PortalUrl)
	})

	t.Run("NotFound", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		sd := newStdDeployments(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		})

		_, err := sd.GetSubscriptionDeployment(*mockCtx.Context, "SUB", "missing")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrDeploymentNotFound)
	})
}

func Test_StdDeployments_ListResourceGroupDeployments_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/resourcegroups/RG1") &&
			strings.Contains(req.URL.Path, "/deployments")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("rgDeploy", armresources.ProvisioningStateFailed)
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armresources.DeploymentListResult{Value: []*armresources.DeploymentExtended{&dep}})
	})

	deployments, err := sd.ListResourceGroupDeployments(*mockCtx.Context, "SUB", "RG1")
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "rgDeploy", deployments[0].Name)
	assert.Equal(t, DeploymentProvisioningStateFailed, deployments[0].ProvisioningState)
}

func Test_StdDeployments_GetResourceGroupDeployment_Coverage3(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		sd := newStdDeployments(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			dep := makeDeploymentExtended("rgDeploy", armresources.ProvisioningStateRunning)
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK, dep)
		})

		d, err := sd.GetResourceGroupDeployment(*mockCtx.Context, "SUB", "RG1", "rgDeploy")
		require.NoError(t, err)
		assert.Equal(t, "rgDeploy", d.Name)
		assert.Equal(t, DeploymentProvisioningStateRunning, d.ProvisioningState)
	})

	t.Run("NotFound", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		sd := newStdDeployments(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		})

		_, err := sd.GetResourceGroupDeployment(*mockCtx.Context, "SUB", "RG1", "missing")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrDeploymentNotFound)
	})
}

func Test_StdDeployments_ListSubscriptionDeploymentOperations_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/operations")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armresources.DeploymentOperationsListResult{
				Value: []*armresources.DeploymentOperation{
					{
						ID:          new("op1"),
						OperationID: new("op1-id"),
						Properties: &armresources.DeploymentOperationProperties{
							ProvisioningState: new("Succeeded"),
						},
					},
				},
			})
	})

	ops, err := sd.ListSubscriptionDeploymentOperations(*mockCtx.Context, "SUB", "deploy1")
	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, "op1-id", *ops[0].OperationID)
}

func Test_StdDeployments_ListResourceGroupDeploymentOperations_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/operations")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armresources.DeploymentOperationsListResult{
				Value: []*armresources.DeploymentOperation{
					{
						ID:          new("op2"),
						OperationID: new("op2-id"),
						Properties: &armresources.DeploymentOperationProperties{
							ProvisioningState: new("Failed"),
						},
					},
				},
			})
	})

	ops, err := sd.ListResourceGroupDeploymentOperations(*mockCtx.Context, "SUB", "RG1", "deploy1")
	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, "op2-id", *ops[0].OperationID)
}

func Test_StdDeployments_DeployToSubscription_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPut &&
			strings.Contains(req.URL.Path, "/deployments/sub-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("sub-deploy", armresources.ProvisioningStateSucceeded)
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, dep)
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	params := azure.ArmParameters{}
	d, err := sd.DeployToSubscription(
		*mockCtx.Context, "SUB", "eastus", "sub-deploy",
		template, params, nil, nil,
	)
	require.NoError(t, err)
	assert.Equal(t, "sub-deploy", d.Name)
	assert.Equal(t, DeploymentProvisioningStateSucceeded, d.ProvisioningState)
}

func Test_StdDeployments_DeployToResourceGroup_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPut &&
			strings.Contains(req.URL.Path, "/deployments/rg-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("rg-deploy", armresources.ProvisioningStateSucceeded)
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, dep)
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	params := azure.ArmParameters{}
	d, err := sd.DeployToResourceGroup(
		*mockCtx.Context, "SUB", "RG1", "rg-deploy",
		template, params, nil, nil,
	)
	require.NoError(t, err)
	assert.Equal(t, "rg-deploy", d.Name)
}

func Test_StdDeployments_WhatIfDeployToSubscription_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/whatIf")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armresources.WhatIfOperationResult{
				Status: new("Succeeded"),
			})
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	result, err := sd.WhatIfDeployToSubscription(
		*mockCtx.Context, "SUB", "eastus", "deploy1", template, nil)
	require.NoError(t, err)
	assert.Equal(t, "Succeeded", *result.Status)
}

func Test_StdDeployments_WhatIfDeployToResourceGroup_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/whatIf")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armresources.WhatIfOperationResult{
				Status: new("Succeeded"),
			})
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	result, err := sd.WhatIfDeployToResourceGroup(
		*mockCtx.Context, "SUB", "RG1", "deploy1", template, nil)
	require.NoError(t, err)
	assert.Equal(t, "Succeeded", *result.Status)
}

func Test_StdDeployments_ValidatePreflightToSubscription_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/validate")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armresources.DeploymentValidateResult{
				Properties: &armresources.DeploymentPropertiesExtended{
					ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
				},
			})
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	err := sd.ValidatePreflightToSubscription(
		*mockCtx.Context, "SUB", "eastus", "deploy1", template, nil, nil, nil)
	require.NoError(t, err)
}

func Test_StdDeployments_ValidatePreflightToResourceGroup_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/validate")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armresources.DeploymentValidateResult{
				Properties: &armresources.DeploymentPropertiesExtended{
					ProvisioningState: to.Ptr(armresources.ProvisioningStateSucceeded),
				},
			})
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	err := sd.ValidatePreflightToResourceGroup(
		*mockCtx.Context, "SUB", "RG1", "deploy1", template, nil, nil, nil)
	require.NoError(t, err)
}

func Test_ConvertFromStandardProvisioningState_AllStates_Coverage3(t *testing.T) {
	// Exercise all provisioning state conversions through GetSubscriptionDeployment
	states := []struct {
		arm      armresources.ProvisioningState
		expected DeploymentProvisioningState
	}{
		{armresources.ProvisioningStateAccepted, DeploymentProvisioningStateAccepted},
		{armresources.ProvisioningStateCanceled, DeploymentProvisioningStateCanceled},
		{armresources.ProvisioningStateCreating, DeploymentProvisioningStateCreating},
		{armresources.ProvisioningStateDeleted, DeploymentProvisioningStateDeleted},
		{armresources.ProvisioningStateDeleting, DeploymentProvisioningStateDeleting},
		{armresources.ProvisioningStateFailed, DeploymentProvisioningStateFailed},
		{armresources.ProvisioningStateNotSpecified, DeploymentProvisioningStateNotSpecified},
		{armresources.ProvisioningStateReady, DeploymentProvisioningStateReady},
		{armresources.ProvisioningStateRunning, DeploymentProvisioningStateRunning},
		{armresources.ProvisioningStateSucceeded, DeploymentProvisioningStateSucceeded},
		{armresources.ProvisioningStateUpdating, DeploymentProvisioningStateUpdating},
	}

	for _, tc := range states {
		t.Run(string(tc.arm), func(t *testing.T) {
			mockCtx := mocks.NewMockContext(t.Context())
			sd := newStdDeployments(mockCtx)

			mockCtx.HttpClient.When(func(req *http.Request) bool {
				return req.Method == http.MethodGet
			}).RespondFn(func(req *http.Request) (*http.Response, error) {
				dep := makeDeploymentExtended("d", tc.arm)
				return mocks.CreateHttpResponseWithBody(req, http.StatusOK, dep)
			})

			d, err := sd.GetSubscriptionDeployment(*mockCtx.Context, "SUB", "d")
			require.NoError(t, err)
			assert.Equal(t, tc.expected, d.ProvisioningState)
		})
	}

	// Test unknown state
	t.Run("Unknown", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		sd := newStdDeployments(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			dep := makeDeploymentExtended("d", armresources.ProvisioningState("SomeNewState"))
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK, dep)
		})

		d, err := sd.GetSubscriptionDeployment(*mockCtx.Context, "SUB", "d")
		require.NoError(t, err)
		assert.Equal(t, DeploymentProvisioningState(""), d.ProvisioningState)
	})
}
