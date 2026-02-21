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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractResourceType(tt.message)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestResourceNotAvailableHandler_WithLocationAndResourceType(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "eastus2")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")

	// Without credentials, the handler falls back to static suggestion
	handler := &ResourceNotAvailableHandler{}
	err := errors.New(
		"LocationIsOfferRestricted: Microsoft.Web/staticSites is not available in eastus2",
	)
	result := handler.Handle(context.Background(), err)

	require.NotNil(t, result)
	assert.Contains(t, result.Message, "Microsoft.Web/staticSites")
	assert.Contains(t, result.Suggestion, "eastus2")
	assert.NotEmpty(t, result.DocUrl)
}

func TestResourceNotAvailableHandler_WithoutLocation(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")

	handler := &ResourceNotAvailableHandler{}
	err := errors.New(
		"SkuNotAvailable: Microsoft.Compute/virtualMachines Standard_D2s_v3",
	)
	result := handler.Handle(context.Background(), err)

	require.NotNil(t, result)
	assert.Contains(t, result.Message, "Microsoft.Compute/virtualMachines")
	assert.Contains(t, result.Suggestion, "azd env set AZURE_LOCATION")
}

func TestResourceNotAvailableHandler_NoResourceType(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "westus")
	t.Setenv("AZURE_SUBSCRIPTION_ID", "")

	handler := &ResourceNotAvailableHandler{}
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
