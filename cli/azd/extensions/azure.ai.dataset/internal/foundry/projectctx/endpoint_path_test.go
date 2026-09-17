// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Requests append their own path to the endpoint, so a segment past the project
// name is not a harmless extra: `/api/projects/p/extra` asks the service for
// `/api/projects/p/extra/datasets`, which comes back as a service failure
// rather than as the endpoint being the wrong shape.
func TestAnEndpointWithMoreThanAProjectIsFlagged(t *testing.T) {
	for _, endpoint := range []string{
		"https://x.services.ai.azure.com/api/projects/p/extra",
		"https://x.services.ai.azure.com/api/projects/p/extra/deeper",
		"https://x.services.ai.azure.com/api/projects/p/datasets",
	} {
		normalized, pathWarning, err := Validate(endpoint)
		require.NoError(t, err, endpoint)
		require.True(t, pathWarning, "%s names more than a project", endpoint)
		require.NotEmpty(t, normalized)
	}
}

// The shapes that are actually a project endpoint stay unflagged, including the
// spellings a reader is likely to paste.
func TestAProjectEndpointIsNotFlagged(t *testing.T) {
	for _, endpoint := range []string{
		"https://x.services.ai.azure.com/api/projects/p",
		"https://x.services.ai.azure.com/api/projects/p/",
		"https://X.services.ai.azure.com/api/projects/my-project",
	} {
		_, pathWarning, err := Validate(endpoint)
		require.NoError(t, err, endpoint)
		require.False(t, pathWarning, "%s is a project endpoint", endpoint)
	}
}
