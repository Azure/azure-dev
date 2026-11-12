// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armdeployments"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func TestConvertToDeploymentParams(t *testing.T) {
	require.Nil(t, convertToDeploymentParams(nil))
	require.Empty(t, convertToDeploymentParams(azure.ArmParameters{}))

	for name, value := range map[string]any{
		"string": "text",
		"bool":   false,
		"number": 42,
		"array":  []any{"a", 1},
		"object": map[string]any{"nested": true},
		"null":   nil,
	} {
		t.Run(name, func(t *testing.T) {
			converted := convertToDeploymentParams(azure.ArmParameters{"input": {Value: value}})
			expected, err := json.Marshal(map[string]any{"input": map[string]any{"value": value}})
			require.NoError(t, err)
			actual, err := json.Marshal(converted)
			require.NoError(t, err)
			require.JSONEq(t, string(expected), string(actual))
			require.Nil(t, converted["input"].Reference)
		})
	}
	reference := &azure.KeyVaultParameterReference{
		KeyVault: azure.KeyVaultReference{
			ID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv",
		},
		SecretName:    "password",
		SecretVersion: "version",
	}
	converted := convertToDeploymentParams(azure.ArmParameters{"secret": {KeyVaultReference: reference}})
	require.Equal(t, reference.KeyVault.ID, *converted["secret"].Reference.KeyVault.ID)
	require.Equal(t, reference.SecretName, *converted["secret"].Reference.SecretName)
	require.Equal(t, reference.SecretVersion, *converted["secret"].Reference.SecretVersion)
	require.Nil(t, converted["secret"].Value)
}

func Test_StandardDeployments_GenerateDeploymentName(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	mockContext.Clock.Set(time.Unix(1683303710, 0))

	deploymentService := NewStandardDeployments(
		mockContext.SubscriptionCredentialProvider,
		mockContext.ArmClientOptions,
		NewResourceService(mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions),
		cloud.AzurePublic(),
		mockContext.Clock,
	)

	tcs := []struct {
		envName  string
		expected string
	}{
		{
			envName:  "simple-name",
			expected: "simple-name-1683303710",
		},
		{
			envName:  "azd-template-test-apim-todo-csharp-sql-swa-func-2750207-2",
			expected: "template-test-apim-todo-csharp-sql-swa-func-2750207-2-1683303710",
		},
	}

	for _, tc := range tcs {
		deploymentName := deploymentService.GenerateDeploymentName(tc.envName)
		assert.Equal(t, tc.expected, deploymentName)
		assert.LessOrEqual(t, len(deploymentName), 64)
	}
}

func TestVoidDeploymentTags(t *testing.T) {
	deployment := &ResourceDeployment{
		Tags: map[string]*string{
			azure.TagKeyAzdEnvName:     new("dev"),
			azure.TagKeyAzdLayerName:   new("main"),
			azure.TagKeyAzdProjectName: new("project-a"),
			"unrelated":                new("value"),
		},
	}

	tags := voidDeploymentTags(deployment)

	require.Equal(t, "dev", *tags[azure.TagKeyAzdEnvName])
	require.Equal(t, "main", *tags[azure.TagKeyAzdLayerName])
	require.Equal(t, "project-a", *tags[azure.TagKeyAzdProjectName])
	require.Equal(t, "down", *tags["azd-deploy-reason"])
	require.NotContains(t, tags, "unrelated")
}

func TestCreatedResourceGroupsFromDeployment(t *testing.T) {
	t.Parallel()

	t.Run("references used when no output resources", func(t *testing.T) {
		mockDeployment := &ResourceDeployment{
			//nolint:lll
			Id: "/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/providers/Microsoft.Resources/deployments/matell-2508-1689982746",
			//nolint:lll
			DeploymentId: "/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/providers/Microsoft.Resources/deployments/matell-2508-1689982746",
			Name:         "matell-2508",
			Type:         "Microsoft.Resources/deployments",
			Tags: map[string]*string{
				"azd-env-name": new("matell-2508"),
			},
			ProvisioningState: DeploymentProvisioningStateFailed,
			Timestamp:         time.Now(),
			Dependencies: []*armdeployments.Dependency{
				{
					//nolint:lll
					ID: new(
						"/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/resourceGroups/matell-2508-rg/providers/Microsoft.Resources/deployments/resources",
					),
					ResourceName: new("resources"),
					ResourceType: new("Microsoft.Resources/deployments"),
					DependsOn: []*armdeployments.BasicDependency{
						{
							//nolint:lll
							ID: new(
								"/subscriptions/faa080af-c1d8-40ad-9cce-e1a450ca5b57/resourceGroups/matell-2508-rg",
							),
							ResourceName: new("matell-2508-rg"),
							ResourceType: new("Microsoft.Resources/resourceGroups"),
						},
					},
				},
			},
		}

		require.Equal(t, []string{"matell-2508-rg"}, createdResourceGroupsFromDeployment(mockDeployment))
	})

	t.Run("succeeded deployment only returns created resource groups", func(t *testing.T) {

		mockDeployment := ResourceDeployment{
			Id:   "DEPLOYMENT_ID",
			Name: "test-env",
			Resources: []*armdeployments.ResourceReference{
				{
					ID: new("/subscriptions/sub-id/resourceGroups/groupA"),
				},
				{
					ID: new(
						"/subscriptions/sub-id/resourceGroups/groupA/providers/" +
							"Microsoft.Storage/storageAccounts/storageAccount",
					),
				},
				{
					ID: new("/subscriptions/sub-id/resourceGroups/groupB"),
				},
				{
					ID: new(
						"/subscriptions/sub-id/resourceGroups/groupB/providers/Microsoft.Web/sites/test",
					),
				},
				{
					ID: new(
						"/subscriptions/sub-id/resourceGroups/groupExisting/providers/" +
							"Microsoft.Storage/storageAccounts/storageAccount",
					),
				},
			},
			ProvisioningState: DeploymentProvisioningStateSucceeded,
			Timestamp:         time.Now(),
		}

		groups := createdResourceGroupsFromDeployment(&mockDeployment)

		sort.Strings(groups)
		require.Equal(t, []string{"groupA", "groupB"}, groups)
	})
}

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

func makeDeploymentExtended(name string, state armdeployments.ProvisioningState) armdeployments.DeploymentExtended {
	now := time.Now()
	return armdeployments.DeploymentExtended{
		ID:       new("/subscriptions/SUB/providers/Microsoft.Resources/deployments/" + name),
		Name:     new(name),
		Type:     new("Microsoft.Resources/deployments"),
		Location: new("eastus"),
		Tags:     map[string]*string{"env": new("test")},
		Properties: &armdeployments.DeploymentPropertiesExtended{
			ProvisioningState: new(state),
			Timestamp:         &now,
			TemplateHash:      new("hash123"),
			Outputs:           nil,
			OutputResources:   []*armdeployments.ResourceReference{},
			Dependencies:      []*armdeployments.Dependency{},
		},
	}
}

func Test_StdDeployments_CalculateTemplateHash(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "calculateTemplateHash")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armdeployments.TemplateHashResult{
				TemplateHash: new("abc123hash"),
			})
	})

	hash, err := sd.CalculateTemplateHash(*mockCtx.Context, "SUB",
		azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`)))
	require.NoError(t, err)
	assert.Equal(t, "abc123hash", hash)
}

func Test_StdDeployments_ListSubscriptionDeployments(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/providers/Microsoft.Resources/deployments")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("deploy1", armdeployments.ProvisioningStateSucceeded)
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armdeployments.DeploymentListResult{Value: []*armdeployments.DeploymentExtended{&dep}})
	})

	deployments, err := sd.ListSubscriptionDeployments(*mockCtx.Context, "SUB")
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "deploy1", deployments[0].Name)
	assert.Equal(t, DeploymentProvisioningStateSucceeded, deployments[0].ProvisioningState)
}

func Test_StdDeployments_GetSubscriptionDeployment(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		sd := newStdDeployments(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.Contains(req.URL.Path, "/deployments/deploy1")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			dep := makeDeploymentExtended("deploy1", armdeployments.ProvisioningStateSucceeded)
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

func Test_StdDeployments_ListSubscriptionDeploymentResources_OnlyCreatedResourceGroups(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(strings.ToLower(req.URL.Path), "/providers/microsoft.resources/deployments/deploy1")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("deploy1", armdeployments.ProvisioningStateSucceeded)
		dep.Properties.OutputResources = []*armdeployments.ResourceReference{
			{ID: new("/subscriptions/SUB/resourceGroups/RG1")},
			{ID: new("/subscriptions/SUB/resourceGroups/RG1/providers/Microsoft.Web/sites/app1")},
			{ID: new("/subscriptions/SUB/resourceGroups/EXISTING/providers/Microsoft.Web/sites/app2")},
		}

		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, dep)
	})

	listedRGs := []string{}
	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(strings.ToLower(req.URL.Path), "/resourcegroups/rg1/resources")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		listedRGs = append(listedRGs, "RG1")

		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, armresources.ResourceListResult{
			Value: []*armresources.GenericResourceExpanded{
				{
					ID:       new("/subscriptions/SUB/resourceGroups/RG1/providers/Microsoft.Web/sites/app1"),
					Name:     new("app1"),
					Type:     new("Microsoft.Web/sites"),
					Location: new("eastus"),
				},
			},
		})
	})

	resources, err := sd.ListSubscriptionDeploymentResources(*mockCtx.Context, "SUB", "deploy1")
	require.NoError(t, err)
	require.Len(t, resources, 2)
	assert.Equal(t, []string{"RG1"}, listedRGs)
	assert.Equal(t, "/subscriptions/SUB/resourceGroups/RG1", *resources[0].ID)
	assert.Equal(t, "/subscriptions/SUB/resourceGroups/RG1/providers/Microsoft.Web/sites/app1", *resources[1].ID)
}

func Test_StdDeployments_ListResourceGroupDeployments(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/resourcegroups/RG1") &&
			strings.Contains(req.URL.Path, "/deployments")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("rgDeploy", armdeployments.ProvisioningStateFailed)
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armdeployments.DeploymentListResult{Value: []*armdeployments.DeploymentExtended{&dep}})
	})

	deployments, err := sd.ListResourceGroupDeployments(*mockCtx.Context, "SUB", "RG1")
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	assert.Equal(t, "rgDeploy", deployments[0].Name)
	assert.Equal(t, DeploymentProvisioningStateFailed, deployments[0].ProvisioningState)
}

func Test_StdDeployments_GetResourceGroupDeployment(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		sd := newStdDeployments(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			dep := makeDeploymentExtended("rgDeploy", armdeployments.ProvisioningStateRunning)
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

func Test_StdDeployments_ListSubscriptionDeploymentOperations(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/operations")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armdeployments.DeploymentOperationsListResult{
				Value: []*armdeployments.DeploymentOperation{
					{
						ID:          new("op1"),
						OperationID: new("op1-id"),
						Properties: &armdeployments.DeploymentOperationProperties{
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

func Test_StdDeployments_ListResourceGroupDeploymentOperations(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/operations")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armdeployments.DeploymentOperationsListResult{
				Value: []*armdeployments.DeploymentOperation{
					{
						ID:          new("op2"),
						OperationID: new("op2-id"),
						Properties: &armdeployments.DeploymentOperationProperties{
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

func Test_StdDeployments_DeployToSubscription(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPut &&
			strings.Contains(req.URL.Path, "/deployments/sub-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("sub-deploy", armdeployments.ProvisioningStateSucceeded)
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

func Test_StdDeployments_DeployToResourceGroup(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPut &&
			strings.Contains(req.URL.Path, "/deployments/rg-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("rg-deploy", armdeployments.ProvisioningStateSucceeded)
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

// shortenDeploymentRetry makes the deployment 404 retry backoff effectively instant for tests
// and restores the production values when the test completes.
func shortenDeploymentRetry(t *testing.T) {
	t.Helper()
	origDelay, origMaxDelay := deploymentRetryDelay, deploymentRetryMaxDelay
	deploymentRetryDelay = time.Millisecond
	deploymentRetryMaxDelay = time.Millisecond
	t.Cleanup(func() {
		deploymentRetryDelay = origDelay
		deploymentRetryMaxDelay = origMaxDelay
	})
}

// Test_StdDeployments_DeployToSubscription_Transient404 verifies that a transient
// HTTP 404 (DeploymentNotFound) returned while polling a just-submitted subscription-scoped
// deployment is retried rather than surfaced as an error. This reproduces the read-after-write
// inconsistency reported in issue #8064 (infra.layers, subscription scope).
func Test_StdDeployments_DeployToSubscription_Transient404(t *testing.T) {
	shortenDeploymentRetry(t)

	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	// PUT accepts the deployment and reports a non-terminal state so the SDK begins polling.
	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/deployments/sub-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("sub-deploy", armdeployments.ProvisioningStateRunning)
		return mocks.CreateHttpResponseWithBody(req, http.StatusCreated, dep)
	})

	// The first poll returns a transient 404; subsequent polls return the succeeded deployment.
	var polls int32
	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/deployments/sub-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&polls, 1) == 1 {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		}
		dep := makeDeploymentExtended("sub-deploy", armdeployments.ProvisioningStateSucceeded)
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, dep)
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	d, err := sd.DeployToSubscription(
		*mockCtx.Context, "SUB", "eastus", "sub-deploy", template, azure.ArmParameters{}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "sub-deploy", d.Name)
	assert.Equal(t, DeploymentProvisioningStateSucceeded, d.ProvisioningState)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&polls), int32(2), "expected the transient 404 poll to be retried")
}

// Test_StdDeployments_DeployToResourceGroup_Transient404 verifies the same transient
// 404 retry behavior for resource-group-scoped deployments.
func Test_StdDeployments_DeployToResourceGroup_Transient404(t *testing.T) {
	shortenDeploymentRetry(t)

	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/deployments/rg-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("rg-deploy", armdeployments.ProvisioningStateRunning)
		return mocks.CreateHttpResponseWithBody(req, http.StatusCreated, dep)
	})

	var polls int32
	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/deployments/rg-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		if atomic.AddInt32(&polls, 1) == 1 {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		}
		dep := makeDeploymentExtended("rg-deploy", armdeployments.ProvisioningStateSucceeded)
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK, dep)
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	d, err := sd.DeployToResourceGroup(
		*mockCtx.Context, "SUB", "RG1", "rg-deploy", template, azure.ArmParameters{}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "rg-deploy", d.Name)
	assert.GreaterOrEqual(t, atomic.LoadInt32(&polls), int32(2), "expected the transient 404 poll to be retried")
}

// Test_StdDeployments_DeployToSubscription_Persistent404 verifies that a deployment
// whose poll keeps returning 404 still fails after the retries are exhausted, so genuine
// DeploymentNotFound errors are not masked.
func Test_StdDeployments_DeployToSubscription_Persistent404(t *testing.T) {
	shortenDeploymentRetry(t)
	origRetries := deploymentRetryMaxRetries
	deploymentRetryMaxRetries = 2
	t.Cleanup(func() { deploymentRetryMaxRetries = origRetries })

	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/deployments/sub-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		dep := makeDeploymentExtended("sub-deploy", armdeployments.ProvisioningStateRunning)
		return mocks.CreateHttpResponseWithBody(req, http.StatusCreated, dep)
	})

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/deployments/sub-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	_, err := sd.DeployToSubscription(
		*mockCtx.Context, "SUB", "eastus", "sub-deploy", template, azure.ArmParameters{}, nil, nil)
	require.Error(t, err)
}

// Test_StdDeployments_DeployToResourceGroup_SubmitTime404 verifies that a 404 returned
// by the initial submit (PUT) is not retried, so a genuine submit-time DeploymentNotFound (e.g. a
// missing resource group) fails fast instead of being delayed by the poll-only 404 retry.
func Test_StdDeployments_DeployToResourceGroup_SubmitTime404(t *testing.T) {
	shortenDeploymentRetry(t)

	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	var puts int32
	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/deployments/rg-deploy")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&puts, 1)
		return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	_, err := sd.DeployToResourceGroup(
		*mockCtx.Context, "SUB", "RG1", "rg-deploy", template, azure.ArmParameters{}, nil, nil)
	require.Error(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&puts), "submit-time 404 must not be retried")
}

func Test_StdDeployments_WhatIfDeployToSubscription(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/whatIf")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armdeployments.WhatIfOperationResult{
				Status: new("Succeeded"),
			})
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	result, err := sd.WhatIfDeployToSubscription(
		*mockCtx.Context, "SUB", "eastus", "deploy1", template, nil)
	require.NoError(t, err)
	assert.Equal(t, "Succeeded", *result.Status)
}

func Test_StdDeployments_WhatIfDeployToResourceGroup(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/whatIf")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armdeployments.WhatIfOperationResult{
				Status: new("Succeeded"),
			})
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	result, err := sd.WhatIfDeployToResourceGroup(
		*mockCtx.Context, "SUB", "RG1", "deploy1", template, nil)
	require.NoError(t, err)
	assert.Equal(t, "Succeeded", *result.Status)
}

func Test_StdDeployments_ValidatePreflightToSubscription(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/validate")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armdeployments.DeploymentValidateResult{
				Properties: &armdeployments.DeploymentPropertiesExtended{
					ProvisioningState: to.Ptr(armdeployments.ProvisioningStateSucceeded),
				},
			})
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	err := sd.ValidatePreflightToSubscription(
		*mockCtx.Context, "SUB", "eastus", "deploy1", template, nil, nil, nil)
	require.NoError(t, err)
}

func Test_StdDeployments_ValidatePreflightToResourceGroup(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	sd := newStdDeployments(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/validate")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armdeployments.DeploymentValidateResult{
				Properties: &armdeployments.DeploymentPropertiesExtended{
					ProvisioningState: to.Ptr(armdeployments.ProvisioningStateSucceeded),
				},
			})
	})

	template := azure.RawArmTemplate(json.RawMessage(`{"$schema":"test"}`))
	err := sd.ValidatePreflightToResourceGroup(
		*mockCtx.Context, "SUB", "RG1", "deploy1", template, nil, nil, nil)
	require.NoError(t, err)
}

func Test_ConvertFromStandardProvisioningState_AllStates(t *testing.T) {
	// Exercise all provisioning state conversions through GetSubscriptionDeployment
	states := []struct {
		arm      armdeployments.ProvisioningState
		expected DeploymentProvisioningState
	}{
		{armdeployments.ProvisioningStateAccepted, DeploymentProvisioningStateAccepted},
		{armdeployments.ProvisioningStateCanceled, DeploymentProvisioningStateCanceled},
		{armdeployments.ProvisioningStateCreating, DeploymentProvisioningStateCreating},
		{armdeployments.ProvisioningStateDeleted, DeploymentProvisioningStateDeleted},
		{armdeployments.ProvisioningStateDeleting, DeploymentProvisioningStateDeleting},
		{armdeployments.ProvisioningStateFailed, DeploymentProvisioningStateFailed},
		{armdeployments.ProvisioningStateNotSpecified, DeploymentProvisioningStateNotSpecified},
		{armdeployments.ProvisioningStateReady, DeploymentProvisioningStateReady},
		{armdeployments.ProvisioningStateRunning, DeploymentProvisioningStateRunning},
		{armdeployments.ProvisioningStateSucceeded, DeploymentProvisioningStateSucceeded},
		{armdeployments.ProvisioningStateUpdating, DeploymentProvisioningStateUpdating},
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
			dep := makeDeploymentExtended("d", armdeployments.ProvisioningState("SomeNewState"))
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK, dep)
		})

		d, err := sd.GetSubscriptionDeployment(*mockCtx.Context, "SUB", "d")
		require.NoError(t, err)
		assert.Equal(t, DeploymentProvisioningState(""), d.ProvisioningState)
	})
}
