// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// TestFromPackageMetadataConstant keeps the core alias and the SDK constant in
// sync so core and extensions agree on the provenance marker.
func TestFromPackageMetadataConstant(t *testing.T) {
	t.Parallel()
	require.Equal(t, "azd.fromPackage", project.MetadataKeyFromPackage)
	require.Equal(t, azdext.ArtifactMetadataKeyFromPackage, project.MetadataKeyFromPackage)
}
