// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ServiceResults_Json_Marshal(t *testing.T) {
	deployResult := &ServiceDeployResult{
		Artifacts: ArtifactCollection{
			{
				Kind:         ArtifactKindDeployment,
				Location:     "https://myapp.azurewebsites.net",
				LocationKind: LocationKindRemote,
				Metadata:     nil,
			},
		},
	}

	jsonBytes, err := json.Marshal(deployResult)
	require.NoError(t, err)
	require.NotEmpty(t, string(jsonBytes))
}

func TestArtifactCollection(t *testing.T) {
	// Create a new service context
	ctx := NewServiceContext()

	// Add some artifacts using the available Add method
	err := ctx.Build.Add(&Artifact{
		Kind:         ArtifactKindDirectory,
		Location:     "/path/to/app.exe",
		LocationKind: LocationKindLocal,
		Metadata:     nil,
	})
	require.NoError(t, err)

	err = ctx.Package.Add(&Artifact{
		Kind:         ArtifactKindArchive,
		Location:     "/path/to/package.zip",
		LocationKind: LocationKindLocal,
		Metadata:     nil,
	})
	require.NoError(t, err)

	err = ctx.Package.Add(&Artifact{
		Kind:         ArtifactKindContainer,
		Location:     "registry.io/myapp:latest",
		LocationKind: LocationKindRemote,
		Metadata:     map[string]string{"digest": "sha256:abc123"},
	})
	require.NoError(t, err)

	// Test finding artifacts using available Find method
	buildArtifacts := ctx.Build.Find()
	require.Len(t, buildArtifacts, 1, "Expected 1 build artifact")
	require.Equal(t, "/path/to/app.exe", buildArtifacts[0].Location)

	// Test package artifacts
	packageArtifacts := ctx.Package.Find()
	require.Len(t, packageArtifacts, 2, "Expected 2 package artifacts")

	// Test that deploy collection is empty
	deployArtifacts := ctx.Deploy.Find()
	require.Len(t, deployArtifacts, 0, "Expected deploy to be empty")
}

func TestArtifactKindEnums(t *testing.T) {
	// Test that all well-known kinds are strings
	kinds := []ArtifactKind{
		ArtifactKindDirectory,
		ArtifactKindArchive,
		ArtifactKindContainer,
		ArtifactKindDeployment,
		ArtifactKindConfig,
		ArtifactKindEndpoint,
		ArtifactKindResource,
	}

	for _, kind := range kinds {
		require.NotEmpty(t, string(kind), "ArtifactKind should not be empty string")
	}

	// Test that string conversion works
	require.Equal(t, "container", string(ArtifactKindContainer))
}
