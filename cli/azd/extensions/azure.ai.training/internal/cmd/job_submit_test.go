// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azure.ai.training/internal/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildJobResource_PreservesOutputAssetRegistration(t *testing.T) {
	resource := buildJobResource(&utils.JobDefinition{
		Command:     "python train.py --output ${{outputs.model}}",
		Environment: "example.azurecr.io/training:latest",
		Compute:     "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/a/computes/c",
		Outputs: map[string]utils.OutputDefinition{
			"model": {
				Type:         "custom_model",
				Mode:         "rw_mount",
				AssetName:    "trained-model",
				AssetVersion: "20260813",
			},
		},
	})

	output := resource.Properties.Outputs["model"]
	assert.Equal(t, "custom_model", output.JobOutputType)
	assert.Equal(t, "ReadWriteMount", output.Mode)
	assert.Equal(t, "trained-model", output.AssetName)
	assert.Equal(t, "20260813", output.AssetVersion)
}

func TestJobSubmit_OffersStorageConnectionOverride(t *testing.T) {
	cmd := newJobSubmitCommand(nil)
	flag := cmd.Flags().Lookup("storage-connection-name")

	require.NotNil(t, flag)
	assert.Equal(t, "", flag.DefValue)
}

func TestResolveStorageConnectionName_FlagOverridesYaml(t *testing.T) {
	assert.Equal(t, "flag-storage", resolveStorageConnectionName(" flag-storage ", "yaml-storage"))
	assert.Equal(t, "yaml-storage", resolveStorageConnectionName("", " yaml-storage "))
}
