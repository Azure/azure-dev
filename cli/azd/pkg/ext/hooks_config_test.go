// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestHooksConfig_UnmarshalYAML(t *testing.T) {
	t.Run("legacy single hook format", func(t *testing.T) {
		const doc = `
preprovision:
  shell: sh
  run: scripts/preprovision.sh
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.NoError(t, err)

		require.Equal(t, HooksConfig{
			"preprovision": {
				{
					Shell: ShellTypeBash,
					Run:   "scripts/preprovision.sh",
				},
			},
		}, hooks)
	})

	t.Run("multiple hook format", func(t *testing.T) {
		const doc = `
preprovision:
  - shell: sh
    run: scripts/preprovision-1.sh
  - shell: sh
    run: scripts/preprovision-2.sh
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.NoError(t, err)

		require.Equal(t, HooksConfig{
			"preprovision": {
				{
					Shell: ShellTypeBash,
					Run:   "scripts/preprovision-1.sh",
				},
				{
					Shell: ShellTypeBash,
					Run:   "scripts/preprovision-2.sh",
				},
			},
		}, hooks)
	})
}

func TestHooksConfig_MarshalYAML(t *testing.T) {
	t.Run("single hook emits object", func(t *testing.T) {
		hooks := HooksConfig{
			"preprovision": {
				{
					Shell: ShellTypeBash,
					Run:   "scripts/preprovision.sh",
				},
			},
		}

		data, err := yaml.Marshal(hooks)
		require.NoError(t, err)

		assert.YAMLEq(t, `
preprovision:
  shell: sh
  run: scripts/preprovision.sh
`, string(data))
	})

	t.Run("multiple hooks emit sequence", func(t *testing.T) {
		hooks := HooksConfig{
			"preprovision": {
				{
					Shell: ShellTypeBash,
					Run:   "scripts/preprovision-1.sh",
				},
				{
					Shell: ShellTypeBash,
					Run:   "scripts/preprovision-2.sh",
				},
			},
		}

		data, err := yaml.Marshal(hooks)
		require.NoError(t, err)

		assert.YAMLEq(t, `
preprovision:
  - shell: sh
    run: scripts/preprovision-1.sh
  - shell: sh
    run: scripts/preprovision-2.sh
`, string(data))
	})
}
