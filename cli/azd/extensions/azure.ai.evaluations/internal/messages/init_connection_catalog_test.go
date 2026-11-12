// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListingSimulationModelConnectionsPreservesServiceRefusals(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			responseErr := refusalFrom(t, status,
				"https://project.services.ai.azure.com/api/projects/p/connections",
				`{"error":{"message":"catalog unavailable"}}`)
			responseErr.ErrorCode = ""
			refused := ServiceRefused(status, responseErr)

			got := ListingSimulationModelConnections(refused)

			assert.Same(t, refused, got)
			service, ok := errors.AsType[*serviceError](got)
			require.True(t, ok)
			assert.Equal(t, fmt.Sprintf("http_%d", status), service.Code())
		})
	}
}

func TestListingSimulationModelConnectionsKeepsForbiddenAsAuthFailure(t *testing.T) {
	refused := ServiceRefused(http.StatusForbidden, refusalFrom(t, http.StatusForbidden,
		"https://project.services.ai.azure.com/api/projects/p/connections",
		`{"error":{"message":"access denied"}}`))

	got := ListingSimulationModelConnections(refused)

	assert.Same(t, refused, got)
	local, ok := errors.AsType[*azdext.LocalError](got)
	require.True(t, ok)
	assert.Equal(t, "auth_failed", local.Code)
	_, isService := errors.AsType[*serviceError](got)
	assert.False(t, isService)
}
