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

func TestSkuNotAvailableHandler_WithLocation(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "eastus")

	handler := NewSkuNotAvailableHandler()
	result := handler.Handle(context.Background(), errors.New("SkuNotAvailable"))

	require.NotNil(t, result)
	assert.Equal(t, "The requested VM size or SKU is not available in this region.", result.Message)
	assert.Contains(t, result.Suggestion, "eastus")
	assert.Contains(t, result.Suggestion, "az vm list-skus")
	assert.NotEmpty(t, result.DocUrl)
}

func TestSkuNotAvailableHandler_WithoutLocation(t *testing.T) {
	t.Setenv("AZURE_LOCATION", "")

	handler := NewSkuNotAvailableHandler()
	result := handler.Handle(context.Background(), errors.New("SkuNotAvailable"))

	require.NotNil(t, result)
	assert.Contains(t, result.Suggestion, "azd env set AZURE_LOCATION")
	assert.NotContains(t, result.Suggestion, "current region")
}
