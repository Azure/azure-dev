package azdcontext

import (
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/stretchr/testify/require"
)

func TestAzdContext_ListEnvironments(t *testing.T) {
	tests := []struct {
		name                string
		setupEnv            []string
		setupDefaultEnv     string
		expectedWithRelPath []contracts.EnvListEnvironment
		expectedErr         error
	}{
		{
			"EmptyDir",
			nil,
			"",
			[]contracts.EnvListEnvironment{},
			nil,
		},
		{
			"NoEnvironments",
			nil,
			"default", // Set this to a non-empty value. This creates a config and the environment directory.
			[]contracts.EnvListEnvironment{},
			nil,
		},
		{
			"WithEnvironments",
			[]string{
				"env1",
				"env2",
			},
			"",
			[]contracts.EnvListEnvironment{
				{
					Name:       "env1",
					IsDefault:  false,
					DotEnvPath: "./.azure/env1/.env",
				},
				{
					Name:       "env2",
					IsDefault:  false,
					DotEnvPath: "./.azure/env2/.env",
				},
			},
			nil,
		},
		{
			"WithEnvironmentsAndDefault",
			[]string{
				"env1",
				"env2",
			},
			"env2",
			[]contracts.EnvListEnvironment{
				{
					Name:       "env1",
					IsDefault:  false,
					DotEnvPath: "./.azure/env1/.env",
				},
				{
					Name:       "env2",
					IsDefault:  true,
					DotEnvPath: "./.azure/env2/.env",
				},
			},
			nil,
		},
		{
			"WithEnvironmentsAndUnknownDefault",
			[]string{
				"env1",
				"env2",
			},
			"unknown",
			[]contracts.EnvListEnvironment{
				{
					Name:       "env1",
					IsDefault:  false,
					DotEnvPath: "./.azure/env1/.env",
				},
				{
					Name:       "env2",
					IsDefault:  false,
					DotEnvPath: "./.azure/env2/.env",
				},
			},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			temp := t.TempDir()
			azdCtx := NewAzdContextWithDirectory(temp)
			if tt.setupDefaultEnv != "" {
				config := configFile{
					Version:            ConfigFileVersion,
					DefaultEnvironment: tt.setupDefaultEnv,
				}
				path := filepath.Join(temp, EnvironmentDirectoryName, ConfigFileName)
				err := writeConfig(path, config)
				require.NoError(t, err)
			}

			for _, env := range tt.setupEnv {
				err := createEnvironment(filepath.Join(temp, EnvironmentDirectoryName), env)
				require.NoError(t, err)
			}

			actual, err := azdCtx.ListEnvironments()
			require.NoError(t, err)

			expectedEnvironments := make([]contracts.EnvListEnvironment, len(tt.expectedWithRelPath))
			for i, expected := range tt.expectedWithRelPath {
				expected.DotEnvPath = filepath.Join(temp, EnvironmentDirectoryName, expected.Name, DotEnvFileName)
				expectedEnvironments[i] = expected
			}
			require.Equal(t, expectedEnvironments, actual)
		})
	}
}
