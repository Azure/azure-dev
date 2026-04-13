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

	t.Run("hook with config block", func(t *testing.T) {
		const doc = `
postprovision:
  run: ./hooks/seed-database.cs
  kind: dotnet
  config:
    configuration: Release
    framework: net10.0
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.NoError(t, err)

		require.Len(t, hooks["postprovision"], 1)
		hook := hooks["postprovision"][0]
		assert.Equal(t, "./hooks/seed-database.cs", hook.Run)
		assert.Equal(t,
			language.HookKindDotNet, hook.Kind,
		)
		require.NotNil(t, hook.Config)
		assert.Equal(t, "Release", hook.Config["configuration"])
		assert.Equal(t, "net10.0", hook.Config["framework"])
	})

	t.Run("multiple hooks with different configs",
		func(t *testing.T) {
			const doc = `
postprovision:
  - run: ./hooks/seed-database.cs
    kind: dotnet
    config:
      configuration: Release
  - run: ./hooks/setup.py
    kind: python
    config:
      virtualEnvName: .venv
`

			var hooks HooksConfig
			err := yaml.Unmarshal([]byte(doc), &hooks)
			require.NoError(t, err)

			require.Len(t, hooks["postprovision"], 2)

			dotnetHook := hooks["postprovision"][0]
			assert.Equal(t,
				"./hooks/seed-database.cs", dotnetHook.Run,
			)
			require.NotNil(t, dotnetHook.Config)
			assert.Equal(t,
				"Release",
				dotnetHook.Config["configuration"],
			)

			pythonHook := hooks["postprovision"][1]
			assert.Equal(t,
				"./hooks/setup.py", pythonHook.Run,
			)
			require.NotNil(t, pythonHook.Config)
			assert.Equal(t,
				".venv",
				pythonHook.Config["virtualEnvName"],
			)
		})

	t.Run("nested config maps", func(t *testing.T) {
		const doc = `
postprovision:
  run: ./hooks/setup.py
  kind: python
  config:
    database:
      host: localhost
      port: 5432
    logging:
      level: debug
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.NoError(t, err)

		require.Len(t, hooks["postprovision"], 1)
		hook := hooks["postprovision"][0]
		require.NotNil(t, hook.Config)

		db, ok := hook.Config["database"].(map[string]any)
		require.True(t, ok,
			"database should be map[string]any",
		)
		assert.Equal(t, "localhost", db["host"])
		assert.Equal(t, 5432, db["port"])

		logging, ok :=
			hook.Config["logging"].(map[string]any)
		require.True(t, ok,
			"logging should be map[string]any",
		)
		assert.Equal(t, "debug", logging["level"])
	})

	t.Run("config with list values", func(t *testing.T) {
		const doc = `
postprovision:
  run: ./hooks/setup.sh
  config:
    paths:
      - ./src
      - ./lib
    flags:
      - --verbose
      - --dry-run
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.NoError(t, err)

		require.Len(t, hooks["postprovision"], 1)
		hook := hooks["postprovision"][0]
		require.NotNil(t, hook.Config)

		paths, ok := hook.Config["paths"].([]any)
		require.True(t, ok,
			"paths should be []any",
		)
		require.Len(t, paths, 2)
		assert.Equal(t, "./src", paths[0])
		assert.Equal(t, "./lib", paths[1])

		flags, ok := hook.Config["flags"].([]any)
		require.True(t, ok,
			"flags should be []any",
		)
		require.Len(t, flags, 2)
		assert.Equal(t, "--verbose", flags[0])
		assert.Equal(t, "--dry-run", flags[1])
	})

	t.Run("platform override hooks with config",
		func(t *testing.T) {
			const doc = `
postprovision:
  run: ./hooks/setup.py
  kind: python
  config:
    virtualEnvName: .venv
  windows:
    run: .\hooks\setup.py
    kind: python
    config:
      virtualEnvName: .win-venv
  posix:
    run: ./hooks/setup.py
    kind: python
    config:
      virtualEnvName: .posix-venv
`

			var hooks HooksConfig
			err := yaml.Unmarshal([]byte(doc), &hooks)
			require.NoError(t, err)

			require.Len(t, hooks["postprovision"], 1)
			hook := hooks["postprovision"][0]

			require.NotNil(t, hook.Config)
			assert.Equal(t,
				".venv",
				hook.Config["virtualEnvName"],
			)

			require.NotNil(t, hook.Windows)
			require.NotNil(t, hook.Windows.Config)
			assert.Equal(t,
				".win-venv",
				hook.Windows.Config["virtualEnvName"],
			)

			require.NotNil(t, hook.Posix)
			require.NotNil(t, hook.Posix.Config)
			assert.Equal(t,
				".posix-venv",
				hook.Posix.Config["virtualEnvName"],
			)
		})

	t.Run("config type preservation",
		func(t *testing.T) {
			const doc = `
postprovision:
  run: ./hooks/setup.sh
  config:
    retries: 3
    verbose: true
    timeout: 30.5
    name: my-hook
`

			var hooks HooksConfig
			err := yaml.Unmarshal([]byte(doc), &hooks)
			require.NoError(t, err)

			require.Len(t, hooks["postprovision"], 1)
			hook := hooks["postprovision"][0]
			require.NotNil(t, hook.Config)

			assert.IsType(t, 0, hook.Config["retries"])
			assert.Equal(t, 3, hook.Config["retries"])

			assert.IsType(t,
				true, hook.Config["verbose"],
			)
			assert.Equal(t,
				true, hook.Config["verbose"],
			)

			assert.IsType(t,
				0.0, hook.Config["timeout"],
			)
			assert.InDelta(t,
				30.5, hook.Config["timeout"], 0.001,
			)

			assert.IsType(t, "", hook.Config["name"])
			assert.Equal(t,
				"my-hook", hook.Config["name"],
			)

			// Roundtrip preserves types.
			data, err := yaml.Marshal(hooks)
			require.NoError(t, err)

			var got HooksConfig
			err = yaml.Unmarshal(data, &got)
			require.NoError(t, err)

			rtHook := got["postprovision"][0]
			require.NotNil(t, rtHook.Config)
			assert.Equal(t, hook.Config, rtHook.Config)
		})

	t.Run("empty config block", func(t *testing.T) {
		const doc = `
postprovision:
  run: ./hooks/setup.sh
  config: {}
`

		var hooks HooksConfig
		err := yaml.Unmarshal([]byte(doc), &hooks)
		require.NoError(t, err)

		require.Len(t, hooks["postprovision"], 1)
		hook := hooks["postprovision"][0]

		require.NotNil(t, hook.Config)
		assert.Empty(t, hook.Config)

		// Marshal omits empty config via omitempty tag.
		data, err := yaml.Marshal(hooks)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "config")
	})

	t.Run("config in list-format hooks",
		func(t *testing.T) {
			const doc = `
postprovision:
  - run: ./hooks/first.py
    kind: python
    config:
      virtualEnvName: .venv
  - run: ./hooks/second.ts
    kind: ts
    config:
      packageManager: pnpm
`

			var hooks HooksConfig
			err := yaml.Unmarshal([]byte(doc), &hooks)
			require.NoError(t, err)

			require.Len(t, hooks["postprovision"], 2)

			first := hooks["postprovision"][0]
			assert.Equal(t,
				"./hooks/first.py", first.Run,
			)
			assert.Equal(t,
				language.HookKindPython, first.Kind,
			)
			require.NotNil(t, first.Config)
			assert.Equal(t,
				".venv",
				first.Config["virtualEnvName"],
			)

			second := hooks["postprovision"][1]
			assert.Equal(t,
				"./hooks/second.ts", second.Run,
			)
			assert.Equal(t,
				language.HookKindTypeScript, second.Kind,
			)
			require.NotNil(t, second.Config)
			assert.Equal(t,
				"pnpm",
				second.Config["packageManager"],
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

	t.Run("roundtrip with config block", func(t *testing.T) {
		hooks := HooksConfig{
			"postprovision": {
				{
					Run:  "./hooks/seed.cs",
					Kind: language.HookKindDotNet,
					Config: map[string]any{
						"configuration": "Release",
						"framework":     "net10.0",
					},
				},
			},
		}

		data, err := yaml.Marshal(hooks)
		require.NoError(t, err)

		var got HooksConfig
		err = yaml.Unmarshal(data, &got)
		require.NoError(t, err)

		require.Len(t, got["postprovision"], 1)
		hook := got["postprovision"][0]
		assert.Equal(t, "./hooks/seed.cs", hook.Run)
		assert.Equal(t,
			language.HookKindDotNet, hook.Kind,
		)
		require.NotNil(t, hook.Config)
		assert.Equal(t,
			"Release", hook.Config["configuration"],
		)
		assert.Equal(t,
			"net10.0", hook.Config["framework"],
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
