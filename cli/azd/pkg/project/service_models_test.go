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
		Kind:             AppServiceTarget,
		TargetResourceId: "target-resource-id",
		Endpoints: []string{
			"https://aka.ms/azd",
		},
		Details: nil,
		Package: &ServicePackageResult{
			Build: &ServiceBuildResult{
				BuildOutputPath: "build/output/path",
				Restore:         &ServiceRestoreResult{},
				Details: &dockerBuildResult{
					ImageId:   "image-id",
					ImageName: "image-name",
				},
			},
			PackagePath: "package/path/project.zip",
			IsTemporary: true, // Test the new field
			Details: &dockerPackageResult{
				ImageHash:   "image-hash",
				TargetImage: "image-tag",
			},
		},
	}

	jsonBytes, err := json.Marshal(deployResult)
	require.NoError(t, err)
	require.NotEmpty(t, string(jsonBytes))

	// Verify IsTemporary field is serialized
	require.Contains(t, string(jsonBytes), `"isTemporary":true`)
}

func Test_ServicePackageResult_IsTemporary(t *testing.T) {
	// Test that IsTemporary defaults to false for zero value
	packageResult := &ServicePackageResult{}
	require.False(t, packageResult.IsTemporary)

	// Test that IsTemporary can be set to true
	packageResult.IsTemporary = true
	require.True(t, packageResult.IsTemporary)

	// Test that IsTemporary can be set to false explicitly
	packageResult.IsTemporary = false
	require.False(t, packageResult.IsTemporary)
}
