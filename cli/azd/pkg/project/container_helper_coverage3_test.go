// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ContainerHelper_DockerfileBuilder_Coverage3(t *testing.T) {
	ch := &ContainerHelper{}
	builder := ch.DockerfileBuilder()
	require.NotNil(t, builder)
}

func Test_getDockerOptionsWithDefaults_Coverage3(t *testing.T) {
	t.Run("AllEmpty", func(t *testing.T) {
		result := getDockerOptionsWithDefaults(DockerProjectOptions{})
		assert.Equal(t, "./Dockerfile", result.Path)
		assert.Equal(t, docker.DefaultPlatform, result.Platform)
		assert.Equal(t, ".", result.Context)
	})

	t.Run("AllSet", func(t *testing.T) {
		opts := DockerProjectOptions{
			Path:     "custom/Dockerfile",
			Platform: "linux/arm64",
			Context:  "./src",
		}
		result := getDockerOptionsWithDefaults(opts)
		assert.Equal(t, "custom/Dockerfile", result.Path)
		assert.Equal(t, "linux/arm64", result.Platform)
		assert.Equal(t, "./src", result.Context)
	})

	t.Run("PartiallySet", func(t *testing.T) {
		opts := DockerProjectOptions{
			Path: "my/Dockerfile",
		}
		result := getDockerOptionsWithDefaults(opts)
		assert.Equal(t, "my/Dockerfile", result.Path)
		assert.Equal(t, docker.DefaultPlatform, result.Platform)
		assert.Equal(t, ".", result.Context)
	})
}
