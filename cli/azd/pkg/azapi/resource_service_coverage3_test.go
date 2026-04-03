// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParseArmResourceID(t *testing.T, id string) arm.ResourceID {
	t.Helper()
	parsed, err := arm.ParseResourceID(id)
	require.NoError(t, err, "failed to parse resource ID: %s", id)
	return *parsed
}

func Test_ResourceService_CheckExistenceByID_Coverage3(t *testing.T) {
	t.Run("Exists", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodHead
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNoContent)
		})

		resID := mustParseArmResourceID(t,
			"/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.Web/sites/app1")
		exists, err := rs.CheckExistenceByID(*mockCtx.Context, resID, "2023-01-01")
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("NotExists", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodHead
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		})

		resID := mustParseArmResourceID(t,
			"/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.Web/sites/app1")
		exists, err := rs.CheckExistenceByID(*mockCtx.Context, resID, "2023-01-01")
		require.NoError(t, err)
		assert.False(t, exists)
	})
}

func Test_ResourceService_GetRawResource_Coverage3(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK, armresources.GenericResource{
				ID: new("RES_ID"), Name: new("RES"), Type: new("Microsoft.Web/sites"),
				Location: new("eastus"), Kind: new("app"),
			})
		})

		resID := mustParseArmResourceID(t,
			"/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.Web/sites/app1")
		raw, err := rs.GetRawResource(*mockCtx.Context, resID, "2023-01-01")
		require.NoError(t, err)
		assert.Contains(t, raw, "RES_ID")
	})

	t.Run("NotFound", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		})

		resID := mustParseArmResourceID(t,
			"/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.Web/sites/app1")
		_, err := rs.GetRawResource(*mockCtx.Context, resID, "2023-01-01")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting resource by id")
	})
}

func Test_ResourceService_ListResourceGroupResources_Coverage3(t *testing.T) {
	t.Run("WithFilter", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.Contains(req.URL.Path, "/resourceGroups/RG1/resources")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK, armresources.ResourceListResult{
				Value: []*armresources.GenericResourceExpanded{
					{
						ID:   new("/subscriptions/SUB/resourceGroups/RG1/providers/Microsoft.Web/sites/app1"),
						Name: new("app1"), Type: new("Microsoft.Web/sites"),
						Location: new("eastus"), Kind: new("app"),
					},
				},
			})
		})

		filter := "resourceType eq 'Microsoft.Web/sites'"
		resources, err := rs.ListResourceGroupResources(
			*mockCtx.Context, "SUB", "RG1",
			&ListResourceGroupResourcesOptions{Filter: &filter},
		)
		require.NoError(t, err)
		require.Len(t, resources, 1)
		assert.Equal(t, "app1", resources[0].Name)
		assert.Equal(t, "app", resources[0].Kind)
	})

	t.Run("NilOptions", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armresources.ResourceListResult{Value: []*armresources.GenericResourceExpanded{}})
		})

		resources, err := rs.ListResourceGroupResources(*mockCtx.Context, "SUB", "RG1", nil)
		require.NoError(t, err)
		assert.Empty(t, resources)
	})
}

func Test_ResourceService_ListResourceGroup_Coverage3(t *testing.T) {
	t.Run("WithTagFilter", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/resourcegroups")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armresources.ResourceGroupListResult{
					Value: []*armresources.ResourceGroup{
						{
							ID:   new("/subscriptions/SUB/resourceGroups/RG1"),
							Name: new("RG1"), Type: new("Microsoft.Resources/resourceGroups"),
							Location: new("eastus"), ManagedBy: new("aks"),
						},
					},
				})
		})

		groups, err := rs.ListResourceGroup(*mockCtx.Context, "SUB", &ListResourceGroupOptions{
			TagFilter: &Filter{Key: "azd-env-name", Value: "my-env"},
		})
		require.NoError(t, err)
		require.Len(t, groups, 1)
		assert.Equal(t, "RG1", groups[0].Name)
		assert.Equal(t, "aks", *groups[0].ManagedBy)
	})

	t.Run("WithFilter", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armresources.ResourceGroupListResult{Value: []*armresources.ResourceGroup{}})
		})

		f := "name eq 'rg'"
		groups, err := rs.ListResourceGroup(*mockCtx.Context, "SUB",
			&ListResourceGroupOptions{Filter: &f})
		require.NoError(t, err)
		assert.Empty(t, groups)
	})

	t.Run("NilOptions", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armresources.ResourceGroupListResult{
					Value: []*armresources.ResourceGroup{
						{
							ID:   new("/subscriptions/SUB/resourceGroups/rg1"),
							Name: new("rg1"), Type: new("Microsoft.Resources/resourceGroups"),
							Location: new("westus"),
						},
					},
				})
		})

		groups, err := rs.ListResourceGroup(*mockCtx.Context, "SUB", nil)
		require.NoError(t, err)
		require.Len(t, groups, 1)
	})
}

func Test_ResourceService_ListSubscriptionResources_Coverage3(t *testing.T) {
	t.Run("WithFilter", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armresources.ResourceListResult{
					Value: []*armresources.GenericResourceExpanded{
						{
							ID:   new("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.Web/sites/app1"),
							Name: new("app1"), Type: new("Microsoft.Web/sites"),
							Location: new("eastus"), Kind: new("app,linux"),
						},
					},
				})
		})

		filter := "resourceType eq 'Microsoft.Web/sites'"
		res, err := rs.ListSubscriptionResources(*mockCtx.Context, "SUB",
			&armresources.ClientListOptions{Filter: &filter})
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, "app1", res[0].Name)
		assert.Equal(t, "app,linux", res[0].Kind)
	})

	t.Run("NilOptions", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armresources.ResourceListResult{Value: []*armresources.GenericResourceExpanded{}})
		})

		res, err := rs.ListSubscriptionResources(*mockCtx.Context, "SUB", nil)
		require.NoError(t, err)
		assert.Empty(t, res)
	})
}

func Test_ResourceService_CreateOrUpdateResourceGroup_Coverage3(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK, armresources.ResourceGroup{
				ID:   new("/subscriptions/SUB/resourceGroups/RG1"),
				Name: new("RG1"), Location: new("eastus"),
			})
		})

		rg, err := rs.CreateOrUpdateResourceGroup(*mockCtx.Context, "SUB", "RG1", "eastus",
			map[string]*string{"env": new("test")})
		require.NoError(t, err)
		assert.Equal(t, "RG1", rg.Name)
		assert.Equal(t, "eastus", rg.Location)
	})

	t.Run("Error", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodPut
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusForbidden)
		})

		_, err := rs.CreateOrUpdateResourceGroup(*mockCtx.Context, "SUB", "RG1", "eastus", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "creating or updating resource group")
	})
}

func Test_ResourceService_DeleteResourceGroup_Coverage3(t *testing.T) {
	t.Run("AlreadyDeleted", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodDelete
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusNotFound)
		})

		err := rs.DeleteResourceGroup(*mockCtx.Context, "SUB", "GONE_RG")
		require.NoError(t, err) // 404 = already deleted
	})

	t.Run("Success", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(context.Background())
		rs := NewResourceService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)
		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodDelete
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateEmptyHttpResponse(req, http.StatusOK)
		})

		err := rs.DeleteResourceGroup(*mockCtx.Context, "SUB", "MY_RG")
		require.NoError(t, err)
	})
}

func Test_GroupByResourceGroup_Coverage3(t *testing.T) {
	t.Run("GroupsCorrectly", func(t *testing.T) {
		resources := []*armresources.ResourceReference{
			{ID: new(
				"/subscriptions/SUB/resourceGroups/rg1/providers/Microsoft.Web/sites/app1")},
			{ID: new(
				"/subscriptions/SUB/resourceGroups/rg2/providers/Microsoft.Storage/storageAccounts/sa1")},
		}
		result, err := GroupByResourceGroup(resources)
		require.NoError(t, err)
		require.Len(t, result, 2)
		assert.Len(t, result["rg1"], 1)
		assert.Equal(t, "app1", result["rg1"][0].Name)
		assert.Len(t, result["rg2"], 1)
		assert.Equal(t, "sa1", result["rg2"][0].Name)
	})

	t.Run("SkipsResourceGroupType", func(t *testing.T) {
		resources := []*armresources.ResourceReference{
			{ID: new(
				"/subscriptions/S/resourceGroups/rg1/providers/Microsoft.Resources/resourceGroups/rg1")},
			{ID: new(
				"/subscriptions/S/resourceGroups/rg1/providers/Microsoft.Web/sites/app1")},
		}
		result, err := GroupByResourceGroup(resources)
		require.NoError(t, err)
		assert.Len(t, result["rg1"], 1)
		assert.Equal(t, "app1", result["rg1"][0].Name)
	})

	t.Run("InvalidResourceID", func(t *testing.T) {
		_, err := GroupByResourceGroup([]*armresources.ResourceReference{
			{ID: new("bad-id")},
		})
		require.Error(t, err)
	})

	t.Run("Empty", func(t *testing.T) {
		result, err := GroupByResourceGroup(nil)
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}
