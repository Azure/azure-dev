// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHookConfig_ExecutorDispatch verifies that every valid combination
// of Kind, Run, and Dir resolves to the correct executor Kind after
// validate(). The Kind string is the key passed to
// serviceLocator.ResolveNamed() in execHook() to select the executor.
func TestHookConfig_ExecutorDispatch(t *testing.T) {
	t.Parallel()

	// expectedOSDefault is the Kind assigned to inline scripts when
	// no explicit Kind or Shell is set.
	expectedOSDefault := language.HookKindBash
	if runtime.GOOS == "windows" {
		expectedOSDefault = language.HookKindPowerShell
	}

	tests := []struct {
		name         string
		kind         language.HookKind // explicit Kind (empty = infer)
		run          string
		dir          string   // explicit Dir (empty = infer)
		createFiles  []string // paths relative to tmpDir
		expectedKind language.HookKind
	}{
		// ── Explicit Kind always wins ──────────────────────────
		{
			name:         "ExplicitKindPython_RunPy",
			kind:         language.HookKindPython,
			run:          "main.py",
			createFiles:  []string{"main.py"},
			expectedKind: language.HookKindPython,
		},
		{
			name:         "ExplicitKindBash_RunSh",
			kind:         language.HookKindBash,
			run:          "deploy.sh",
			createFiles:  []string{"deploy.sh"},
			expectedKind: language.HookKindBash,
		},
		{
			name:         "ExplicitKindOverridesExtension",
			kind:         language.HookKindJavaScript,
			run:          "main.py",
			createFiles:  []string{"main.py"},
			expectedKind: language.HookKindJavaScript,
		},

		// ── Run only (no dir, no kind) — infer from extension ─
		{
			name:         "RunOnly_Py",
			run:          "main.py",
			createFiles:  []string{"main.py"},
			expectedKind: language.HookKindPython,
		},
		{
			name:         "RunOnly_Sh",
			run:          "deploy.sh",
			createFiles:  []string{"deploy.sh"},
			expectedKind: language.HookKindBash,
		},
		{
			name:         "RunOnly_Ps1",
			run:          "deploy.ps1",
			createFiles:  []string{"deploy.ps1"},
			expectedKind: language.HookKindPowerShell,
		},
		{
			name:         "RunOnly_Js",
			run:          "index.js",
			createFiles:  []string{"index.js"},
			expectedKind: language.HookKindJavaScript,
		},
		{
			name:         "RunOnly_Ts",
			run:          "index.ts",
			createFiles:  []string{"index.ts"},
			expectedKind: language.HookKindTypeScript,
		},
		{
			name:         "RunOnly_Cs",
			run:          "Program.cs",
			createFiles:  []string{"Program.cs"},
			expectedKind: language.HookKindDotNet,
		},

		// ── Dir + Run (no kind) — infer from extension ────────
		{
			name:         "DirRun_Py",
			run:          "main.py",
			dir:          filepath.Join("hooks", "pre"),
			createFiles:  []string{filepath.Join("hooks", "pre", "main.py")},
			expectedKind: language.HookKindPython,
		},
		{
			name:         "DirRun_Sh",
			run:          "deploy.sh",
			dir:          filepath.Join("hooks", "pre"),
			createFiles:  []string{filepath.Join("hooks", "pre", "deploy.sh")},
			expectedKind: language.HookKindBash,
		},
		{
			name:         "DirRun_Ps1",
			run:          "deploy.ps1",
			dir:          filepath.Join("hooks", "pre"),
			createFiles:  []string{filepath.Join("hooks", "pre", "deploy.ps1")},
			expectedKind: language.HookKindPowerShell,
		},
		{
			name:         "DirRun_Js",
			run:          "index.js",
			dir:          filepath.Join("hooks", "pre"),
			createFiles:  []string{filepath.Join("hooks", "pre", "index.js")},
			expectedKind: language.HookKindJavaScript,
		},
		{
			name:         "DirRun_Ts",
			run:          "index.ts",
			dir:          filepath.Join("hooks", "pre"),
			createFiles:  []string{filepath.Join("hooks", "pre", "index.ts")},
			expectedKind: language.HookKindTypeScript,
		},
		{
			name:         "DirRun_Cs",
			run:          "Program.cs",
			dir:          filepath.Join("hooks", "pre"),
			createFiles:  []string{filepath.Join("hooks", "pre", "Program.cs")},
			expectedKind: language.HookKindDotNet,
		},

		// ── Dir + Run with subdirectory path in run ───────────
		{
			name:         "DirRun_NestedPy",
			run:          filepath.Join("src", "main.py"),
			dir:          "hooks",
			createFiles:  []string{filepath.Join("hooks", "src", "main.py")},
			expectedKind: language.HookKindPython,
		},

		// ── Inline scripts (no file) — default to OS shell ────
		{
			name:         "Inline_NoFile",
			run:          "echo hello",
			expectedKind: expectedOSDefault,
		},
		{
			name:         "Inline_DirSet_NoFile",
			run:          "echo hello",
			dir:          filepath.Join("hooks", "pre"),
			expectedKind: expectedOSDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmpDir := t.TempDir()

			for _, f := range tt.createFiles {
				fullPath := filepath.Join(tmpDir, f)
				require.NoError(t,
					os.MkdirAll(filepath.Dir(fullPath), 0o755))
				require.NoError(t,
					os.WriteFile(fullPath, []byte("# placeholder"), 0o600))
			}

			config := HookConfig{
				Name:     "test",
				Run:      tt.run,
				Dir:      tt.dir,
				Kind:     tt.kind,
				inputCwd: tmpDir,
			}

			err := config.validate()
			require.NoError(t, err)

			assert.Equal(t, tt.expectedKind, config.Kind,
				"expected Kind %q but got %q for ResolveNamed() executor lookup",
				tt.expectedKind, config.Kind)
		})
	}
}

// TestHookKind_ExecutorRegistrationNames verifies that the string
// representation of each HookKind matches the key used when
// registering executors in the IoC container. These are the exact
// strings passed to serviceLocator.ResolveNamed() in execHook().
// If someone renames a constant, this test ensures the IoC
// lookup string stays in sync.
func TestHookKind_ExecutorRegistrationNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "sh", string(language.HookKindBash),
		"Bash executor registration key")
	assert.Equal(t, "pwsh", string(language.HookKindPowerShell),
		"PowerShell executor registration key")
	assert.Equal(t, "python", string(language.HookKindPython),
		"Python executor registration key")
	assert.Equal(t, "js", string(language.HookKindJavaScript),
		"JavaScript executor registration key")
	assert.Equal(t, "ts", string(language.HookKindTypeScript),
		"TypeScript executor registration key")
	assert.Equal(t, "dotnet", string(language.HookKindDotNet),
		"DotNet executor registration key")
}
