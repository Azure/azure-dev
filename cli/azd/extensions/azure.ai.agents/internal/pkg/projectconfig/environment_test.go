// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadServiceEnvironment(t *testing.T) {
	t.Parallel()

	t.Run("preserves expressions and converts scalars", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeProjectFile(t, root, `services:
  agent:
    host: azure.ai.agent
    env:
      PROJECT: ${{project.endpoint}}
      ENABLED: true
      RETRIES: 3
      RATIO: 1.5
      EMPTY:
`)

		env, err := LoadServiceEnvironment(root, "agent")

		require.NoError(t, err)
		assert.Equal(t, "${{project.endpoint}}", env["PROJECT"])
		assert.Equal(t, "true", env["ENABLED"])
		assert.Equal(t, "3", env["RETRIES"])
		assert.Equal(t, "1.5", env["RATIO"])
		assert.Equal(t, "", env["EMPTY"])
	})

	t.Run("resolves service file references", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeProjectFile(t, root, `services:
  agent:
    $ref: ./agent.yaml
`)
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "agent.yaml"),
			[]byte(`host: azure.ai.agent
env:
  CONNECTION: ${{connections.search.credentials.key}}
`),
			0o600,
		))

		env, err := LoadServiceEnvironment(root, "agent")

		require.NoError(t, err)
		assert.Equal(
			t,
			"${{connections.search.credentials.key}}",
			env["CONNECTION"],
		)
	})

	for _, test := range []struct {
		name  string
		value string
	}{
		{"map", "      BAD:\n        nested: value\n"},
		{"sequence", "      BAD:\n        - value\n"},
	} {
		t.Run("rejects "+test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeProjectFile(t, root, "services:\n"+
				"  agent:\n"+
				"    host: azure.ai.agent\n"+
				"    env:\n"+
				test.value)

			_, err := LoadServiceEnvironment(root, "agent")

			require.ErrorContains(t, err, "must be a scalar")
		})
	}
}

func writeProjectFile(t *testing.T, root string, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(contents),
		0o600,
	))
}
