// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestLoadEffectiveAgentEnvironment_InlineAndServiceEnv(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeAzureYAML(t, root, `
services:
  echo:
    host: azure.ai.agent
    env:
      SERVICE_ONLY: ${SERVICE_VALUE}
      SHARED: service
      NUMBER: 42
`)
	svc := newAgentService(t, map[string]any{
		"kind": "hostedAgent",
		"environmentVariables": []any{
			map[string]any{"name": "DEFINITION_ONLY", "value": "definition"},
			map[string]any{"name": "SHARED", "value": "definition"},
		},
	})

	got, err := loadEffectiveAgentEnvironment(svc, root)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"DEFINITION_ONLY": "definition",
		"SERVICE_ONLY":    "${SERVICE_VALUE}",
		"SHARED":          "service",
		"NUMBER":          "42",
	}, got)
	assert.Equal(t,
		[]string{"definition", "42", "${SERVICE_VALUE}", "service"},
		sortedEnvironmentValues(got),
	)
}

func TestLoadEffectiveAgentEnvironment_RootRefAndSiblingEnvironment(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeAzureYAML(t, root, `
services:
  echo:
    host: azure.ai.agent
    $ref: ./service.yaml
    env:
      SERVICE_VALUE: from-service
`)
	writeProjectFile(t, root, "service.yaml", `
kind: hostedAgent
environmentVariables:
  - name: FROM_REF
    value: ref
  - name: OVERRIDDEN
    value: ref
`)
	svc := newAgentService(t, map[string]any{
		"$ref":        "./service.yaml",
		"description": "sibling",
		"environmentVariables": []any{
			map[string]any{"name": "OVERRIDDEN", "value": "sibling"},
		},
	})

	got, err := loadEffectiveAgentEnvironment(svc, root)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"OVERRIDDEN":    "sibling",
		"SERVICE_VALUE": "from-service",
	}, got)
}

func TestLoadEffectiveAgentEnvironment_DeprecatedConfigFallback(t *testing.T) {
	t.Parallel()

	svc := &azdext.ServiceConfig{
		Name: "echo",
		Host: agentHost,
		Config: mustStruct(t, map[string]any{
			"kind": "hostedAgent",
			"environmentVariables": []any{
				map[string]any{"name": "CONFIG_VALUE", "value": "${CONFIG_VALUE}"},
			},
		}),
	}

	got, err := loadEffectiveAgentEnvironment(svc, t.TempDir())

	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"CONFIG_VALUE": "${CONFIG_VALUE}",
	}, got)
}

func TestLoadEffectiveAgentEnvironment_IgnoresLegacyKeyInServiceProperties(t *testing.T) {
	t.Parallel()

	svc := newAgentService(t, map[string]any{
		"kind": "hostedAgent",
		"environment_variables": []any{
			map[string]any{"name": "LEGACY_VALUE", "value": "${LEGACY_VALUE}"},
		},
	})

	got, err := loadEffectiveAgentEnvironment(svc, t.TempDir())

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestLoadEffectiveAgentEnvironment_LegacyFilesFallback(t *testing.T) {
	t.Parallel()

	for _, fileName := range []string{"agent.yaml", "agent.yml"} {
		t.Run(fileName, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeProjectFile(t, root, fileName, `
kind: hostedAgent
environment_variables:
  - name: LEGACY_VALUE
    value: ${LEGACY_VALUE}
`)
			svc := &azdext.ServiceConfig{
				Name:         "echo",
				Host:         agentHost,
				RelativePath: ".",
			}

			got, err := loadEffectiveAgentEnvironment(svc, root)

			require.NoError(t, err)
			assert.Equal(t, map[string]string{
				"LEGACY_VALUE": "${LEGACY_VALUE}",
			}, got)
		})
	}
}

func TestLoadEffectiveAgentEnvironment_InlineWinsLegacyFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "agent.yaml", `
kind: hostedAgent
environment_variables:
  - name: VALUE
    value: stale
`)
	svc := newAgentService(t, map[string]any{
		"kind": "hostedAgent",
		"environmentVariables": []any{
			map[string]any{"name": "VALUE", "value": "inline"},
		},
	})

	got, err := loadEffectiveAgentEnvironment(svc, root)

	require.NoError(t, err)
	assert.Equal(t, map[string]string{"VALUE": "inline"}, got)
}

func TestLoadEffectiveAgentEnvironment_InvalidConfiguration(t *testing.T) {
	t.Parallel()

	t.Run("invalid ref", func(t *testing.T) {
		t.Parallel()
		svc := newAgentService(t, map[string]any{
			"$ref": "./missing.yaml",
		})

		_, err := loadEffectiveAgentEnvironment(svc, t.TempDir())

		require.Error(t, err)
		assert.ErrorContains(t, err, "resolve service-level properties")
	})

	t.Run("malformed legacy yaml", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeProjectFile(t, root, "agent.yaml", "kind: [broken")
		svc := &azdext.ServiceConfig{
			Name:         "echo",
			Host:         agentHost,
			RelativePath: ".",
		}

		_, err := loadEffectiveAgentEnvironment(svc, root)

		require.Error(t, err)
		assert.ErrorContains(t, err, "parse legacy agent file")
	})

	t.Run("invalid legacy service path", func(t *testing.T) {
		t.Parallel()
		svc := &azdext.ServiceConfig{
			Name:         "echo",
			Host:         agentHost,
			RelativePath: "../outside",
		}

		_, err := loadEffectiveAgentEnvironment(svc, t.TempDir())

		require.Error(t, err)
		assert.ErrorContains(t, err, "resolve legacy agent file path")
		assert.ErrorContains(t, err, "must not contain '..'")
	})

	t.Run("non-scalar service env", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeAzureYAML(t, root, `
services:
  echo:
    host: azure.ai.agent
    env:
      INVALID:
        - value
`)
		svc := &azdext.ServiceConfig{Name: "echo", Host: agentHost}

		_, err := loadEffectiveAgentEnvironment(svc, root)

		require.Error(t, err)
		assert.ErrorContains(t, err, `env "INVALID"`)
	})
}

func newAgentService(t *testing.T, properties map[string]any) *azdext.ServiceConfig {
	t.Helper()
	return &azdext.ServiceConfig{
		Name:                 "echo",
		Host:                 agentHost,
		AdditionalProperties: mustStruct(t, properties),
	}
}

func mustStruct(t *testing.T, values map[string]any) *structpb.Struct {
	t.Helper()
	structure, err := structpb.NewStruct(values)
	require.NoError(t, err)
	return structure
}

func writeAzureYAML(t *testing.T, root, content string) {
	t.Helper()
	writeProjectFile(t, root, "azure.yaml", content)
}

func writeProjectFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}
