// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvResolver_LayerPriority(t *testing.T) {
	resolver := &EnvResolver{
		osEnv:  map[string]string{"A": "os", "B": "os"},
		azdEnv: map[string]string{"B": "azd", "C": "azd"},
	}

	sc := &ScriptConfig{
		Env:     map[string]string{"C": "env-val", "D": "env-only"},
		Secrets: map[string]string{"D": "secret-wins"},
	}

	env, err := resolver.Resolve(sc)
	require.NoError(t, err)

	require.Equal(t, "os", env["A"], "Layer 1: OS env")
	require.Equal(t, "azd", env["B"], "Layer 2: azd overrides OS")
	require.Equal(t, "env-val", env["C"], "Layer 3: env map overrides azd")
	require.Equal(t, "secret-wins", env["D"], "Layer 4: secrets override env")
}

func TestEnvResolver_ExpressionSubstitution(t *testing.T) {
	resolver := &EnvResolver{
		osEnv:  map[string]string{},
		azdEnv: map[string]string{"ENV_NAME": "dev", "LOCATION": "eastus"},
	}

	sc := &ScriptConfig{
		Env: map[string]string{
			"RESOURCE_GROUP": "rg-${ENV_NAME}",
			"FULL":           "${ENV_NAME}-${LOCATION}",
		},
	}

	env, err := resolver.Resolve(sc)
	require.NoError(t, err)
	require.Equal(t, "rg-dev", env["RESOURCE_GROUP"])
	require.Equal(t, "dev-eastus", env["FULL"])
}

func TestEnvResolver_MergeOutputs(t *testing.T) {
	resolver := &EnvResolver{
		osEnv:  map[string]string{},
		azdEnv: map[string]string{"A": "before"},
	}

	resolver.MergeOutputs(map[string]OutputParameter{
		"B": {Type: "string", Value: "from-script"},
	})

	sc := &ScriptConfig{
		Env: map[string]string{"X": "${B}"},
	}

	env, err := resolver.Resolve(sc)
	require.NoError(t, err)
	require.Equal(t, "from-script", env["B"], "merged output available")
	require.Equal(t, "from-script", env["X"], "merged output usable in expressions")
}

func TestEnvResolver_NilEnvAndSecrets(t *testing.T) {
	resolver := &EnvResolver{
		osEnv:  map[string]string{"A": "os"},
		azdEnv: map[string]string{},
	}

	sc := &ScriptConfig{}

	env, err := resolver.Resolve(sc)
	require.NoError(t, err)
	require.Equal(t, "os", env["A"])
}
