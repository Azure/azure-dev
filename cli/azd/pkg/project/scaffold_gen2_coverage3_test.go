// Copyright (c) Microsoft Corporation. Licensed under the MIT License.
// Tests for scaffold_gen.go mapAppService function
package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_mapAppService_Coverage3(t *testing.T) {
	t.Run("MissingRuntimeStack", func(t *testing.T) {
		res := &ResourceConfig{
			Name: "web",
			Props: AppServiceProps{
				Runtime: AppServiceRuntime{
					Stack:   "",
					Version: "3.11",
				},
			},
		}
		svcSpec := &scaffold.ServiceSpec{}
		infraSpec := &scaffold.InfraSpec{}
		svcConfig := &ServiceConfig{}

		err := mapAppService(res, svcSpec, infraSpec, svcConfig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runtime.type is required")
	})

	t.Run("MissingRuntimeVersion", func(t *testing.T) {
		res := &ResourceConfig{
			Name: "web",
			Props: AppServiceProps{
				Runtime: AppServiceRuntime{
					Stack:   "python",
					Version: "",
				},
			},
		}
		svcSpec := &scaffold.ServiceSpec{}
		infraSpec := &scaffold.InfraSpec{}
		svcConfig := &ServiceConfig{}

		err := mapAppService(res, svcSpec, infraSpec, svcConfig)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runtime.version is required")
	})

	t.Run("ValidPythonService", func(t *testing.T) {
		res := &ResourceConfig{
			Name: "web",
			Props: AppServiceProps{
				Runtime: AppServiceRuntime{
					Stack:   "python",
					Version: "3.11",
				},
				Port:           8080,
				StartupCommand: "gunicorn app:app",
				Env: []ServiceEnvVar{
					{Name: "APP_ENV", Value: "production"},
				},
			},
		}
		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		infraSpec := &scaffold.InfraSpec{}
		svcConfig := &ServiceConfig{Language: ServiceLanguagePython}

		err := mapAppService(res, svcSpec, infraSpec, svcConfig)
		require.NoError(t, err)
		require.NotNil(t, svcSpec.Runtime)
		assert.Equal(t, "python", svcSpec.Runtime.Type)
		assert.Equal(t, "3.11", svcSpec.Runtime.Version)
		assert.Equal(t, "gunicorn app:app", svcSpec.StartupCommand)
		assert.Equal(t, 8080, svcSpec.Port)
	})

	t.Run("ValidNodeService", func(t *testing.T) {
		res := &ResourceConfig{
			Name: "api",
			Props: AppServiceProps{
				Runtime: AppServiceRuntime{
					Stack:   "node",
					Version: "18-lts",
				},
				Port: 3000,
				Env: []ServiceEnvVar{
					{Name: "NODE_ENV", Value: "production"},
				},
			},
		}
		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		infraSpec := &scaffold.InfraSpec{}
		svcConfig := &ServiceConfig{Language: ServiceLanguageJavaScript}

		err := mapAppService(res, svcSpec, infraSpec, svcConfig)
		require.NoError(t, err)
		require.NotNil(t, svcSpec.Runtime)
		assert.Equal(t, "node", svcSpec.Runtime.Type)
		assert.Equal(t, "18-lts", svcSpec.Runtime.Version)
		assert.Equal(t, 3000, svcSpec.Port)
	})

	t.Run("NoEnvVars", func(t *testing.T) {
		res := &ResourceConfig{
			Name: "simple",
			Props: AppServiceProps{
				Runtime: AppServiceRuntime{
					Stack:   "dotnet",
					Version: "8.0",
				},
				Port: 80,
			},
		}
		svcSpec := &scaffold.ServiceSpec{Env: map[string]string{}}
		infraSpec := &scaffold.InfraSpec{}
		svcConfig := &ServiceConfig{}

		err := mapAppService(res, svcSpec, infraSpec, svcConfig)
		require.NoError(t, err)
		assert.Equal(t, "dotnet", svcSpec.Runtime.Type)
		assert.Equal(t, 80, svcSpec.Port)
	})
}
