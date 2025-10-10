// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ServiceResults_Json_Marshal(t *testing.T) {
	// Create artifacts using the new model
	buildArtifacts := ArtifactCollection{
		NewArtifact(ArtifactKindExecutable, "build/output/app.exe", "", map[string]string{"version": "1.0.0"}),
	}

	packageArtifacts := ArtifactCollection{
		NewArtifact(ArtifactKindArchive, "package/path/project.zip", "", nil),
		NewArtifact(ArtifactKindContainer, "", "registry.io/myapp:latest", map[string]string{
			"imageHash":   "image-hash",
			"targetImage": "image-tag",
		}),
	}

	deployResult := &ServiceDeployResult{
		Kind:             AppServiceTarget,
		TargetResourceId: "target-resource-id",
		Endpoints: []string{
			"https://aka.ms/azd",
		},
		Package: &ServicePackageResult{
			Build: &ServiceBuildResult{
				Restore:   &ServiceRestoreResult{Artifacts: ArtifactCollection{}},
				Artifacts: buildArtifacts,
			},
			Artifacts: packageArtifacts,
		},
		Artifacts: ArtifactCollection{
			NewArtifact(ArtifactKindDeployment, "", "https://myapp.azurewebsites.net", nil),
		},
	}

	jsonBytes, err := json.Marshal(deployResult)
	require.NoError(t, err)
	require.NotEmpty(t, string(jsonBytes))
}

func TestArtifactCollection(t *testing.T) {
	// Create a new service context
	ctx := NewServiceContext()

	// Add some artifacts using the new methods
	ctx.Build.AddWithKind(ArtifactKindExecutable, "/path/to/app.exe", "", nil)
	ctx.Build.AddWithKind(ArtifactKindLibrary, "/path/to/lib.dll", "", map[string]string{"version": "1.0.0"})

	err := ctx.Package.Add(NewArtifact(ArtifactKindArchive, "/path/to/package.zip", "", nil))
	require.NoError(t, err)
	err = ctx.Package.Add(NewArtifact(ArtifactKindContainer, "", "registry.io/myapp:latest", map[string]string{"digest": "sha256:abc123"}))
	require.NoError(t, err)

	ctx.Publish.AddWithKind(ArtifactKindContainer, "", "myregistry.azurecr.io/myapp:v1.0", nil)

	// Test finding artifacts
	executable := ctx.Build.FindByKind(ArtifactKindExecutable)
	require.NotNil(t, executable, "Expected to find executable artifact")
	require.Equal(t, "/path/to/app.exe", executable.Location)

	// Test filtering artifacts
	containerImages := ctx.Package.FilterByKind(ArtifactKindContainer)
	require.Len(t, containerImages, 1, "Expected 1 container image artifact")

	// Test HasKind
	require.True(t, ctx.Build.HasKind(ArtifactKindLibrary), "Expected build to have library artifact")
	require.False(t, ctx.Deploy.HasKind(ArtifactKindExecutable), "Expected deploy to not have executable artifact")

	// Test GetPrimaryLocation
	buildLocation := ctx.Build.GetPrimaryLocation()
	require.Equal(t, "/path/to/app.exe", buildLocation)

	// Test GetPrimaryLocationByKind
	containerLocation := ctx.Package.GetPrimaryLocationByKind(ArtifactKindContainer)
	require.Equal(t, "registry.io/myapp:latest", containerLocation)

	// Test custom kinds still work
	ctx.Extras["custom"] = ArtifactCollection{}
	customCollection := ctx.Extras["custom"]
	customCollection.Add(NewArtifactWithKindString("custom-artifact-type", "/custom/path", "", nil))
	ctx.Extras["custom"] = customCollection

	customArtifact := ctx.Extras["custom"].FindByKindString("custom-artifact-type")
	require.NotNil(t, customArtifact, "Expected to find custom artifact")
}

func TestArtifactKindEnums(t *testing.T) {
	// Test that all well-known kinds are strings
	kinds := []ArtifactKind{
		ArtifactKindExecutable,
		ArtifactKindArchive,
		ArtifactKindContainer,
		ArtifactKindBlob,
		ArtifactKindHelmChart,
		ArtifactKindDeployment,
		ArtifactKindFile,
	}

	for _, kind := range kinds {
		require.NotEmpty(t, string(kind), "ArtifactKind should not be empty string")
	}

	// Test that string conversion works
	require.Equal(t, "container-image", string(ArtifactKindContainer))
}
