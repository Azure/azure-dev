// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseJobFile_ReadsSDKCompatibleFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
command: echo ok
environment: example.azurecr.io/training:latest
compute: gpu
storage_connection_name: project-storage
outputs:
  model:
    type: custom_model
    mode: rw_mount
    asset_name: trained-model
    asset_version: "20260813"
`), 0o600))

	job, err := ParseJobFile(path)
	require.NoError(t, err)
	assert.Equal(t, "project-storage", job.StorageConnectionName)
	assert.Equal(t, "trained-model", job.Outputs["model"].AssetName)
	assert.Equal(t, "20260813", job.Outputs["model"].AssetVersion)
}

func TestResolveRelativePaths_PreservesAzureAIURI(t *testing.T) {
	uri := "azureai://accounts/account/projects/project/data/training/versions/1"
	job := &JobDefinition{
		Code: uri,
		Inputs: map[string]InputDefinition{
			"training": {Type: "uri_folder", Path: uri},
		},
	}

	job.ResolveRelativePaths(t.TempDir())

	assert.Equal(t, uri, job.Code)
	assert.Equal(t, uri, job.Inputs["training"].Path)
}
