// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProjectConnectionsUseTheFoundryProjectAPIVersion(t *testing.T) {
	assert.Equal(t, "2025-11-15-preview", ProjectConnectionsAPIVersion)
	assert.Equal(t, ProjectEndpointAPIVersion, ProjectConnectionsAPIVersion)
}
