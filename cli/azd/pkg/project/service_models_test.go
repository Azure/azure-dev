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
			Details: &dockerPackageResult{
				ImageHash:   "image-hash",
				TargetImage: "image-tag",
			},
		},
	}

	jsonBytes, err := json.Marshal(deployResult)
	require.NoError(t, err)
	require.NotEmpty(t, string(jsonBytes))
}
