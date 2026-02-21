// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package errorhandler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractResourceType(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected string
	}{
		{
			name:     "static web app",
			message:  "The resource type 'Microsoft.Web/staticSites' is not available in location 'eastus2'.",
			expected: "Microsoft.Web/staticSites",
		},
		{
			name:     "cognitive services",
			message:  "Microsoft.CognitiveServices/accounts is restricted in eastus",
			expected: "Microsoft.CognitiveServices/accounts",
		},
		{
			name:     "compute VMs",
			message:  "SkuNotAvailable: Microsoft.Compute/virtualMachines size Standard_D2s_v3",
			expected: "Microsoft.Compute/virtualMachines",
		},
		{
			name:     "no resource type",
			message:  "some generic error without a resource type",
			expected: "",
		},
		{
			name: "prefers quoted resource type over URL",
			message: "POST https://management.azure.com/subscriptions/xxx/" +
				"providers/Microsoft.Resources/deployments/my-deploy/validate\n" +
				"The provided location 'eastus' is not available for " +
				"resource type 'Microsoft.Web/staticSites'.",
			expected: "Microsoft.Web/staticSites",
		},
		{
			name: "real full error with URL",
			message: "deployment failed: error deploying infrastructure: " +
				"validating deployment to subscription:\n\n" +
				"Validation Error Details:\n" +
				"POST https://management.azure.com/subscriptions/4d042dc6/" +
				"providers/Microsoft.Resources/deployments/test/validate\n" +
				"RESPONSE 400: 400 Bad Request\n" +
				"ERROR CODE: LocationNotAvailableForResourceType\n" +
				`{"error":{"code":"LocationNotAvailableForResourceType",` +
				`"message":"The provided location 'eastus' is not available ` +
				`for resource type 'Microsoft.Web/staticSites'."}}`,
			expected: "Microsoft.Web/staticSites",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractResourceType(tt.message)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResourceNotAvailableHandler_WithLocationAndResourceType(t *testing.T) {
	handler := &ResourceNotAvailableHandler{
		env: &mockEnv{values: map[string]string{
			"AZURE_LOCATION": "eastus2",
		}},
	}
	err := errors.New(
		"LocationIsOfferRestricted: Microsoft.Web/staticSites is not available in eastus2",
	)
	result := handler.Handle(context.Background(), err)

	require.NotNil(t, result)
	assert.Contains(t, result.Message, "Microsoft.Web/staticSites")
	assert.Contains(t, result.Suggestion, "eastus2")
	assert.NotEmpty(t, result.Links)
}

func TestResourceNotAvailableHandler_WithoutLocation(t *testing.T) {
	handler := &ResourceNotAvailableHandler{
		env: &mockEnv{values: map[string]string{}},
	}
	err := errors.New(
		"SkuNotAvailable: Microsoft.Compute/virtualMachines Standard_D2s_v3",
	)
	result := handler.Handle(context.Background(), err)

	require.NotNil(t, result)
	assert.Contains(t, result.Message, "Microsoft.Compute/virtualMachines")
	assert.Contains(t, result.Suggestion, "azd env set AZURE_LOCATION")
}

func TestResourceNotAvailableHandler_NoResourceType(t *testing.T) {
	handler := &ResourceNotAvailableHandler{
		env: &mockEnv{values: map[string]string{
			"AZURE_LOCATION": "westus",
		}},
	}
	err := errors.New("LocationIsOfferRestricted: offer restricted in this region")
	result := handler.Handle(context.Background(), err)

	require.NotNil(t, result)
	assert.Equal(t, "A resource type is not available in the current region.",
		result.Message)
	assert.Contains(t, result.Suggestion, "westus")
}

func TestResourceNotAvailableHandler_BuildSuggestion_WithLocations(t *testing.T) {
	handler := &ResourceNotAvailableHandler{}
	err := errors.New("test error")

	result := handler.buildSuggestion(
		err,
		"eastus2",
		"Microsoft.Web/staticSites",
		[]string{"centralus", "eastus", "westus2"},
	)

	require.NotNil(t, result)
	assert.Contains(t, result.Message, "Microsoft.Web/staticSites")
	assert.Contains(t, result.Suggestion, "eastus2")
	assert.Contains(t, result.Suggestion, "centralus, eastus, westus2")
	assert.Contains(t, result.Suggestion, "azd env set AZURE_LOCATION")
}

func TestResourceNotAvailableHandler_BuildSuggestion_WithLocationsNoCurrentRegion(t *testing.T) {
	handler := &ResourceNotAvailableHandler{}
	err := errors.New("test error")

	result := handler.buildSuggestion(
		err,
		"",
		"Microsoft.Web/staticSites",
		[]string{"centralus", "eastus"},
	)

	require.NotNil(t, result)
	assert.Contains(t, result.Suggestion, "centralus, eastus")
	assert.NotContains(t, result.Suggestion, "current region")
}

func TestResourceNotAvailableHandler_LocationNotAvailableForResourceType(t *testing.T) {
	// Simulate the real ARM validation error for Static Web Apps
	realError := errors.New(
		`The provided location 'eastus' is not available for ` +
			`resource type 'Microsoft.Web/staticSites'. ` +
			`List of available regions for the resource type is ` +
			`'westus2,centralus,eastus2,westeurope,eastasia'.`,
	)

	mockResolver := &mockLocationResolver{
		locations: []string{
			"centralus", "eastasia", "eastus2",
			"westeurope", "westus2",
		},
	}
	handler := &ResourceNotAvailableHandler{
		locationResolver: mockResolver,
		env: &mockEnv{values: map[string]string{
			"AZURE_LOCATION":        "eastus",
			"AZURE_SUBSCRIPTION_ID": "test-sub-id",
		}},
	}

	result := handler.Handle(context.Background(), realError)

	require.NotNil(t, result)
	assert.Contains(t, result.Message, "Microsoft.Web/staticSites")
	assert.Contains(t, result.Suggestion, "eastus")
	assert.Contains(t, result.Suggestion, "centralus")
	assert.Contains(t, result.Suggestion, "westus2")
	assert.Contains(t, result.Suggestion, "azd env set AZURE_LOCATION")

	// Verify the resolver was called with the correct resource type
	assert.Equal(t, "Microsoft.Web/staticSites", mockResolver.lastResourceType)
}

// mockLocationResolver implements ResourceTypeLocationResolver for testing.
type mockLocationResolver struct {
	locations        []string
	err              error
	lastResourceType string
}

func (m *mockLocationResolver) GetLocations(
	_ context.Context, _ string, resourceType string,
) ([]string, error) {
	m.lastResourceType = resourceType
	return m.locations, m.err
}

// mockEnv implements EnvironmentResolver for testing.
type mockEnv struct {
	values map[string]string
}

func (m *mockEnv) Getenv(key string) string {
	return m.values[key]
}
