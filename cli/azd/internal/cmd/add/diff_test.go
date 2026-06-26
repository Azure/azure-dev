// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func TestFormatLine_AllOps(t *testing.T) {
	t.Parallel()
	oldM := map[string]*project.ResourceConfig{
		"keep":    {Name: "keep", Type: project.ResourceTypeDbRedis},
		"changed": {Name: "changed", Type: project.ResourceTypeDbPostgres},
	}
	newM := map[string]*project.ResourceConfig{
		"keep":    {Name: "keep", Type: project.ResourceTypeDbRedis},
		"changed": {Name: "changed", Type: project.ResourceTypeDbMongo},
		"added":   {Name: "added", Type: project.ResourceTypeKeyVault},
	}
	s, err := DiffBlocks(oldM, newM)
	require.NoError(t, err)
	assert.NotEmpty(t, s)
}
