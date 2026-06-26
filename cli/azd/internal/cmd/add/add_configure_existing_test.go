// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func TestResourceType_KnownAndUnknown(t *testing.T) {
	t.Parallel()
	// Known
	got := resourceType("Microsoft.Cache/redis")
	assert.Equal(t, project.ResourceTypeDbRedis, got)

	got = resourceType("Microsoft.KeyVault/vaults")
	assert.Equal(t, project.ResourceTypeKeyVault, got)

	// Unknown
	got = resourceType("Microsoft.DoesNot/exist")
	assert.Equal(t, project.ResourceType(""), got)
}
