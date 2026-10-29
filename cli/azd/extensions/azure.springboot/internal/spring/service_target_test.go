// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestRemoteContainerImageSelectsRemoteContainerArtifact(t *testing.T) {
	image, err := remoteContainerImage([]*azdext.Artifact{
		{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
			Location:     "maven-package",
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		},
		{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER,
			Location:     "registry.example/petclinic:tag",
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			Metadata:     map[string]string{"remoteImage": "registry.example/petclinic:tag"},
		},
	})

	require.NoError(t, err)
	require.Equal(t, "registry.example/petclinic:tag", image)
}

func TestPublishedContainerImageDoesNotHideInvalidArtifacts(t *testing.T) {
	_, err := publishedContainerImage(
		&azdext.ServiceContext{Publish: []*azdext.Artifact{{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_DIRECTORY,
			Location:     "not-an-image",
			LocationKind: azdext.LocationKind_LOCATION_KIND_LOCAL,
		}}},
		map[string]string{
			serviceImageEnvironmentKey("petclinic"): "registry.example/stale:tag",
		},
		"petclinic",
	)

	require.ErrorContains(t, err, "no remote image")
}
