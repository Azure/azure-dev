// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
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
					Shell: string(language.HookKindBash),
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
					Shell: string(language.HookKindBash),
					Run:   "scripts/preprovision-1.sh",
				},
				{
					Shell: string(language.HookKindBash),
					Run:   "scripts/preprovision-2.sh",
				},
			},
		}, hooks)
	})

	t.Run("mixed formats in same block", func(t *testing.T) {
		const doc = `
preprovision:
  - run: ./hooks/preprovision.sh
    shell: sh
  - run: ./hooks/preprovision/main.py
predeploy:
  windows:
    shell: pwsh
    run: 'echo "VITE_API_BASE_URL=$env:API_BASE_URL" > ./src/web/.env.local'
  posix:
    shell: sh
    run: 'echo VITE_API_BASE_URL=\"$API_BASE_URL\" > ./src/web/.env.local'
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.NoError(t, err)

		require.Len(t, hooks["preprovision"], 2)
		assert.Equal(t, "./hooks/preprovision.sh", hooks["preprovision"][0].Run)
		assert.Equal(t, string(language.HookKindBash), hooks["preprovision"][0].Shell)
		assert.Equal(t, "./hooks/preprovision/main.py", hooks["preprovision"][1].Run)

		require.Len(t, hooks["predeploy"], 1)
		require.NotNil(t, hooks["predeploy"][0].Windows)
		assert.Equal(t, string(language.HookKindPowerShell), hooks["predeploy"][0].Windows.Shell)
		require.NotNil(t, hooks["predeploy"][0].Posix)
		assert.Equal(t, string(language.HookKindBash), hooks["predeploy"][0].Posix.Shell)
	})

	t.Run("empty hooks block", func(t *testing.T) {
		const doc = `{}`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.NoError(t, err)

		assert.Empty(t, hooks)
	})

	t.Run("invalid yaml in hook entry", func(t *testing.T) {
		const doc = `
preprovision:
  - [not, valid, hook]
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal hook")
	})

	t.Run("null hook entry", func(t *testing.T) {
		const doc = `
preprovision:
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.NoError(t, err)

		require.Contains(t, hooks, "preprovision")
		require.Len(t, hooks["preprovision"], 1)
		assert.Nil(t, hooks["preprovision"][0])
	})

	t.Run("empty list", func(t *testing.T) {
		const doc = `
preprovision: []
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.NoError(t, err)

		require.Contains(t, hooks, "preprovision")
		assert.Empty(t, hooks["preprovision"])
	})

	t.Run("scalar value produces error", func(t *testing.T) {
		const doc = `
preprovision: "just a string"
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.Error(t, err)
		assert.Contains(t,
			err.Error(), "expected mapping or sequence",
		)
	})

	t.Run("multiple hooks with platform overrides",
		func(t *testing.T) {
			yamlDoc := "predeploy:\n" +
				"  - run: setup.sh\n" +
				"    shell: sh\n" +
				"  - windows:\n" +
				"      shell: pwsh\n" +
				"      run: 'echo envvar=$env:VALUE > .env'\n" +
				"    posix:\n" +
				"      shell: sh\n" +
				"      run: 'echo envvar=$VALUE > .env'\n" +
				"  - run: finalize.sh\n" +
				"    shell: sh\n"

			var hooks HooksConfig
			err := yaml.Unmarshal([]byte(yamlDoc), &hooks)
			require.NoError(t, err)

			require.Len(t, hooks["predeploy"], 3)

			first := hooks["predeploy"][0]
			assert.Equal(t, "setup.sh", first.Run)
			assert.Equal(t,
				string(language.HookKindBash), first.Shell,
			)
			assert.Nil(t, first.Windows)
			assert.Nil(t, first.Posix)

			middle := hooks["predeploy"][1]
			require.NotNil(t, middle.Windows)
			assert.Equal(t,
				string(language.HookKindPowerShell),
				middle.Windows.Shell,
			)
			assert.Contains(t,
				middle.Windows.Run, "$env:VALUE",
			)
			require.NotNil(t, middle.Posix)
			assert.Equal(t,
				string(language.HookKindBash),
				middle.Posix.Shell,
			)
			assert.Contains(t, middle.Posix.Run, "$VALUE")

			last := hooks["predeploy"][2]
			assert.Equal(t, "finalize.sh", last.Run)
			assert.Equal(t,
				string(language.HookKindBash), last.Shell,
			)
			assert.Nil(t, last.Windows)
			assert.Nil(t, last.Posix)
		})

	t.Run("three events with mixed formats",
		func(t *testing.T) {
			const doc = `
preprovision:
  - run: script1.sh
    shell: sh
  - run: script2.sh
    shell: sh
postprovision:
  run: script3.sh
  shell: sh
predeploy:
  - run: script4.sh
    shell: sh
`

			var hooks HooksConfig
			err := yaml.Unmarshal([]byte(doc), &hooks)
			require.NoError(t, err)

			require.Len(t, hooks["preprovision"], 2)
			assert.Equal(t,
				"script1.sh", hooks["preprovision"][0].Run,
			)
			assert.Equal(t,
				"script2.sh", hooks["preprovision"][1].Run,
			)

			require.Len(t, hooks["postprovision"], 1)
			assert.Equal(t,
				"script3.sh",
				hooks["postprovision"][0].Run,
			)

			require.Len(t, hooks["predeploy"], 1)
			assert.Equal(t,
				"script4.sh", hooks["predeploy"][0].Run,
			)
		})
}

func TestHooksConfig_MarshalYAML(t *testing.T) {
	t.Run("single hook emits object", func(t *testing.T) {
		hooks := HooksConfig{
			"preprovision": {
				{
					Shell: string(language.HookKindBash),
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
					Shell: string(language.HookKindBash),
					Run:   "scripts/preprovision-1.sh",
				},
				{
					Shell: string(language.HookKindBash),
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

	t.Run("roundtrip with platform overrides",
		func(t *testing.T) {
			hooks := HooksConfig{
				"predeploy": {
					{
						Windows: &HookConfig{
							Shell: string(
								language.HookKindPowerShell,
							),
							Run: `echo "$env:VALUE" > .env`,
						},
						Posix: &HookConfig{
							Shell: string(
								language.HookKindBash,
							),
							Run: `echo "$VALUE" > .env`,
						},
					},
				},
				"preprovision": {
					{
						Shell: string(language.HookKindBash),
						Run:   "scripts/prep-1.sh",
					},
					{
						Shell: string(language.HookKindBash),
						Run:   "scripts/prep-2.sh",
					},
				},
			}

			data, err := yaml.Marshal(hooks)
			require.NoError(t, err)

			var got HooksConfig
			err = yaml.Unmarshal(data, &got)
			require.NoError(t, err)

			// Single hook with overrides stays single.
			require.Len(t, got["predeploy"], 1)
			rt := got["predeploy"][0]
			require.NotNil(t, rt.Windows)
			assert.Equal(t,
				string(language.HookKindPowerShell),
				rt.Windows.Shell,
			)
			assert.Equal(t,
				`echo "$env:VALUE" > .env`, rt.Windows.Run,
			)
			require.NotNil(t, rt.Posix)
			assert.Equal(t,
				string(language.HookKindBash),
				rt.Posix.Shell,
			)
			assert.Equal(t,
				`echo "$VALUE" > .env`, rt.Posix.Run,
			)

			// Multiple hooks remain multiple.
			require.Len(t, got["preprovision"], 2)
			assert.Equal(t,
				"scripts/prep-1.sh",
				got["preprovision"][0].Run,
			)
			assert.Equal(t,
				"scripts/prep-2.sh",
				got["preprovision"][1].Run,
			)
		})
}

func TestHooksConfig_RoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		yamlDoc string
	}{
		{
			name: "single hook with platform overrides",
			yamlDoc: "predeploy:\n" +
				"  windows:\n" +
				"    shell: pwsh\n" +
				"    run: setup.ps1\n" +
				"  posix:\n" +
				"    shell: sh\n" +
				"    run: setup.sh\n",
		},
		{
			name: "multiple hooks in a list",
			yamlDoc: "preprovision:\n" +
				"  - shell: sh\n" +
				"    run: scripts/step1.sh\n" +
				"  - shell: sh\n" +
				"    run: scripts/step2.sh\n",
		},
		{
			name: "mixed single hook and list",
			yamlDoc: "postprovision:\n" +
				"  shell: sh\n" +
				"  run: scripts/post.sh\n" +
				"predeploy:\n" +
				"  - shell: sh\n" +
				"    run: scripts/deploy1.sh\n" +
				"  - shell: sh\n" +
				"    run: scripts/deploy2.sh\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hooks1 HooksConfig
			err := yaml.Unmarshal(
				[]byte(tt.yamlDoc), &hooks1,
			)
			require.NoError(t, err)

			data, err := yaml.Marshal(hooks1)
			require.NoError(t, err)

			var hooks2 HooksConfig
			err = yaml.Unmarshal(data, &hooks2)
			require.NoError(t, err)

			assert.Equal(t, hooks1, hooks2)
		})
	}
}
