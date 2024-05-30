package ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ParseConfig(t *testing.T) {
	t.Run("Success", func(t *testing.T) {

		config := map[string]any{
			"workspace": "my-workspace",
			"flow": map[string]any{
				"name": "my-flow",
				"path": "path/to/flow",
			},
			"environment": map[string]any{
				"name": "my-env",
				"path": "path/to/env.yaml",
			},
			"model": map[string]any{
				"name": "my-model",
				"path": "path/to/model.yaml",
			},
			"deployment": map[string]any{
				"name": "my-deployment",
				"path": "path/to/deployment.yaml",
				"overrides": map[string]any{
					"key": "value",
				},
			},
		}

		noop := func(value string) string { return "" }

		var endpointConfig *EndpointDeploymentConfig
		endpointConfig, err := ParseConfig[EndpointDeploymentConfig](config)
		require.NoError(t, err)
		require.NotNil(t, endpointConfig)

		require.Equal(t, "my-workspace", endpointConfig.Workspace.MustEnvsubst(noop))
		require.NotNil(t, endpointConfig.Environment)
		require.Equal(t, "my-env", endpointConfig.Environment.Name.MustEnvsubst(noop))
		require.Equal(t, "path/to/env.yaml", endpointConfig.Environment.Path)
		require.NotNil(t, endpointConfig.Model)
		require.Equal(t, "my-model", endpointConfig.Model.Name.MustEnvsubst(noop))
		require.Equal(t, "path/to/model.yaml", endpointConfig.Model.Path)
		require.NotNil(t, endpointConfig.Flow)
		require.Equal(t, "my-flow", endpointConfig.Flow.Name.MustEnvsubst(noop))
		require.Equal(t, "path/to/flow", endpointConfig.Flow.Path)
		require.NotNil(t, endpointConfig.Deployment)
		require.Equal(t, "my-deployment", endpointConfig.Deployment.Name.MustEnvsubst(noop))
		require.Equal(t, "path/to/deployment.yaml", endpointConfig.Deployment.Path)
	})

	t.Run("Error", func(t *testing.T) {
		config := "invalid structure"

		var endpointConfig *EndpointDeploymentConfig
		endpointConfig, err := ParseConfig[EndpointDeploymentConfig](config)
		require.Error(t, err)
		require.Nil(t, endpointConfig)
	})
}
