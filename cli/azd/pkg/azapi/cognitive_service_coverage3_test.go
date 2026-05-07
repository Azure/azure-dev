// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_AzureClient_GetAiModels_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/models")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armcognitiveservices.ModelListResult{
				Value: []*armcognitiveservices.Model{
					{
						Model: &armcognitiveservices.AccountModel{
							Name:    new("gpt-4"),
							Format:  new("OpenAI"),
							Version: new("0613"),
						},
						Kind: new("OpenAI"),
					},
					{
						Model: &armcognitiveservices.AccountModel{
							Name:    new("gpt-35-turbo"),
							Format:  new("OpenAI"),
							Version: new("0301"),
						},
						Kind: new("OpenAI"),
					},
				},
			})
	})

	models, err := client.GetAiModels(*mockCtx.Context, "SUB", "eastus")
	require.NoError(t, err)
	require.Len(t, models, 2)
	assert.Equal(t, "gpt-4", *models[0].Model.Name)
	assert.Equal(t, "gpt-35-turbo", *models[1].Model.Name)
}

func Test_AzureClient_GetAiUsages_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(t.Context())
	client := newAzureClientFromMockContext(mockCtx)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/usages")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armcognitiveservices.UsageListResult{
				Value: []*armcognitiveservices.Usage{
					{
						Name:         &armcognitiveservices.MetricName{Value: new("tokens")},
						CurrentValue: to.Ptr[float64](1000),
						Limit:        to.Ptr[float64](10000),
					},
				},
			})
	})

	usages, err := client.GetAiUsages(*mockCtx.Context, "SUB", "eastus")
	require.NoError(t, err)
	require.Len(t, usages, 1)
	assert.Equal(t, float64(1000), *usages[0].CurrentValue)
}

func Test_AzureClient_GetResourceSkuLocations_Coverage3(t *testing.T) {
	t.Run("Found", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		client := newAzureClientFromMockContext(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet &&
				strings.Contains(req.URL.Path, "/skus")
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armcognitiveservices.ResourceSKUListResult{
					Value: []*armcognitiveservices.ResourceSKU{
						{
							Kind:         new("OpenAI"),
							Name:         new("S0"),
							Tier:         new("Standard"),
							ResourceType: new("accounts"),
							Locations:    []*string{new("EastUS"), new("WestUS")},
						},
						{
							Kind:         new("OpenAI"),
							Name:         new("S0"),
							Tier:         new("Standard"),
							ResourceType: new("accounts"),
							Locations:    []*string{new("EastUS")}, // duplicate
						},
						{
							Kind:         new("SpeechServices"),
							Name:         new("F0"),
							Tier:         new("Free"),
							ResourceType: new("accounts"),
							Locations:    []*string{new("NorthEurope")},
						},
					},
				})
		})

		locations, err := client.GetResourceSkuLocations(
			*mockCtx.Context, "SUB", "OpenAI", "S0", "Standard", "accounts")
		require.NoError(t, err)
		assert.Len(t, locations, 2)
		// should be sorted and lowercase
		assert.Equal(t, "eastus", locations[0])
		assert.Equal(t, "westus", locations[1])
	})

	t.Run("NotFound", func(t *testing.T) {
		mockCtx := mocks.NewMockContext(t.Context())
		client := newAzureClientFromMockContext(mockCtx)

		mockCtx.HttpClient.When(func(req *http.Request) bool {
			return req.Method == http.MethodGet
		}).RespondFn(func(req *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
				armcognitiveservices.ResourceSKUListResult{
					Value: []*armcognitiveservices.ResourceSKU{},
				})
		})

		_, err := client.GetResourceSkuLocations(
			*mockCtx.Context, "SUB", "OpenAI", "S0", "Standard", "accounts")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no locations found")
	})
}
