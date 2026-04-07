// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_GetAllHookConfigs(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	t.Run("With Valid Configuration", func(t *testing.T) {
		hooksMap := map[string][]*HookConfig{
			"preinit": {
				{
					Run: "scripts/preinit.sh",
				},
			},
			"postinit": {
				{
					Run: "scripts/postinit.sh",
				},
			},
		}

		ensureScriptsExist(t, hooksMap)

		mockCommandRunner := mockexec.NewMockCommandRunner()
		hooksManager := NewHooksManager(tempDir, mockCommandRunner)
		validHooks, err := hooksManager.GetAll(hooksMap)

		require.Len(t, validHooks, len(hooksMap))
		require.NoError(t, err)
	})

	t.Run("With Invalid Configuration", func(t *testing.T) {
		// All hooksMap are invalid because they are missing the run parameter
		hooksMap := map[string][]*HookConfig{
			"preinit": {
				{
					Shell: string(language.HookKindBash),
					// Run is missing - this should cause an error
				},
			},
			"postinit": {
				{
					Shell: string(language.HookKindBash),
					// Run is missing - this should cause an error
				},
			},
		}

		ensureScriptsExist(t, hooksMap)

		mockCommandRunner := mockexec.NewMockCommandRunner()
		hooksManager := NewHooksManager(tempDir, mockCommandRunner)
		validHooks, err := hooksManager.GetAll(hooksMap)

		require.Nil(t, validHooks)
		require.Error(t, err)
	})

	t.Run("With Missing Configuration", func(t *testing.T) {
		// All hooksMap are invalid because they are missing a script type
		hooksMap := map[string][]*HookConfig{
			"preprovision": nil,
		}

		mockCommandRunner := mockexec.NewMockCommandRunner()
		hooksManager := NewHooksManager(tempDir, mockCommandRunner)
		validHooks, err := hooksManager.GetAll(hooksMap)

		require.NoError(t, err)
		require.NotNil(t, validHooks)
		require.Len(t, validHooks, 0)
	})
}

func Test_GetByParams(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	t.Run("With Valid Configuration", func(t *testing.T) {
		hooksMap := map[string][]*HookConfig{
			"preinit": {
				{
					Run: "scripts/preinit.sh",
				},
			},
			"postinit": {
				{
					Run: "scripts/postinit.sh",
				},
			},
		}

		ensureScriptsExist(t, hooksMap)

		mockCommandRunner := mockexec.NewMockCommandRunner()
		hooksManager := NewHooksManager(tempDir, mockCommandRunner)
		validHooks, err := hooksManager.GetByParams(hooksMap, HookTypePre, "init")

		require.Len(t, validHooks, 1)
		require.Equal(t, hooksMap["preinit"][0], validHooks[0])
		require.NoError(t, err)
	})

	t.Run("With Invalid Configuration", func(t *testing.T) {
		// All hooksMap are invalid because they are missing the run parameter
		hooksMap := map[string][]*HookConfig{
			"preinit": {
				{
					Shell: string(language.HookKindBash),
					// Run is missing - this should cause an error
				},
			},
			"postinit": {
				{
					Shell: string(language.HookKindBash),
					// Run is missing - this should cause an error
				},
			},
		}

		ensureScriptsExist(t, hooksMap)

		mockCommandRunner := mockexec.NewMockCommandRunner()
		hooksManager := NewHooksManager(tempDir, mockCommandRunner)
		validHooks, err := hooksManager.GetByParams(hooksMap, HookTypePre, "init")

		require.Nil(t, validHooks)
		require.Error(t, err)
	})
}

func Test_HookConfig_DefaultShell(t *testing.T) {
	tests := []struct {
		name             string
		hookConfig       *HookConfig
		expectedKind     language.HookKind
		expectingDefault bool
	}{
		{
			name: "No shell specified - should use OS default",
			hookConfig: &HookConfig{
				Name: "test",
				Run:  "echo 'hello'",
			},
			expectedKind:     defaultKindForOS(),
			expectingDefault: true,
		},
		{
			name: "Shell explicitly specified - should not use default",
			hookConfig: &HookConfig{
				Name:  "test",
				Shell: string(language.HookKindBash),
				Run:   "echo 'hello'",
			},
			expectedKind:     language.HookKindBash,
			expectingDefault: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clone the config to avoid modifying the test case
			config := *tt.hookConfig
			config.cwd = t.TempDir()

			err := config.validate()
			require.NoError(t, err)

			require.Equal(t, tt.expectedKind, config.Kind)
			require.Equal(t, tt.expectingDefault, config.IsUsingDefaultShell())
		})
	}
}

func Test_ValidateHooks_PythonInstalled(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	// Create a Python script file so validate() resolves it
	// as a language hook.
	scriptDir := filepath.Join(tempDir, "hooks")
	require.NoError(t, os.MkdirAll(scriptDir, osutil.PermissionDirectory))
	require.NoError(t,
		os.WriteFile(
			filepath.Join(scriptDir, "setup.py"),
			[]byte("print('hello')"), osutil.PermissionExecutableFile,
		),
	)

	hooksMap := map[string][]*HookConfig{
		"preprovision": {
			{Run: "hooks/setup.py"},
		},
	}

	mockRunner := mockexec.NewMockCommandRunner()

	// Mock Python as available: ToolInPath succeeds and
	// --version returns a valid version.
	mockRunner.MockToolInPath("py", nil)
	mockRunner.When(func(args exec.RunArgs, cmd string) bool {
		return strings.Contains(cmd, "--version")
	}).Respond(exec.RunResult{Stdout: "Python 3.12.0"})

	mgr := NewHooksManager(tempDir, mockRunner)
	result := mgr.ValidateHooks(t.Context(), hooksMap)

	// No runtime warnings should be present.
	for _, w := range result.Warnings {
		require.NotContains(t, w.Message, "Python",
			"expected no Python warning when runtime is installed")
	}

	// Also verify the error-returning variant.
	require.NoError(t,
		mgr.ValidateRuntimesErr(t.Context(), hooksMap))
}

func Test_ValidateHooks_PythonNotInstalled(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	scriptDir := filepath.Join(tempDir, "hooks")
	require.NoError(t, os.MkdirAll(scriptDir, osutil.PermissionDirectory))
	require.NoError(t,
		os.WriteFile(
			filepath.Join(scriptDir, "setup.py"),
			[]byte("print('hello')"), osutil.PermissionExecutableFile,
		),
	)

	hooksMap := map[string][]*HookConfig{
		"preprovision": {
			{Run: "hooks/setup.py"},
		},
	}

	mockRunner := mockexec.NewMockCommandRunner()

	// Mock Python as NOT available on any platform path.
	mockRunner.MockToolInPath("py", osexec.ErrNotFound)
	mockRunner.MockToolInPath("python", osexec.ErrNotFound)
	mockRunner.MockToolInPath("python3", osexec.ErrNotFound)

	mgr := NewHooksManager(tempDir, mockRunner)
	result := mgr.ValidateHooks(t.Context(), hooksMap)

	// Expect a warning about missing Python.
	require.NotEmpty(t, result.Warnings,
		"expected at least one warning for missing Python")

	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "Python") {
			found = true
			require.Contains(t, w.Message, "preprovision")
			require.Contains(t, w.Suggestion, "python")
			break
		}
	}
	require.True(t, found, "expected a Python-related warning")

	// Verify the error-returning variant surfaces an
	// ErrorWithSuggestion.
	err := mgr.ValidateRuntimesErr(t.Context(), hooksMap)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Python")
}

func Test_ValidateHooks_ShellHookNoValidation(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	// Create Bash scripts only — no non-shell hooks.
	scriptDir := filepath.Join(tempDir, "scripts")
	require.NoError(t, os.MkdirAll(scriptDir, osutil.PermissionDirectory))
	require.NoError(t,
		os.WriteFile(
			filepath.Join(scriptDir, "pre.sh"),
			nil, osutil.PermissionExecutableFile,
		),
	)
	require.NoError(t,
		os.WriteFile(
			filepath.Join(scriptDir, "post.ps1"),
			nil, osutil.PermissionExecutableFile,
		),
	)

	hooksMap := map[string][]*HookConfig{
		"preprovision": {
			{Run: "scripts/pre.sh"},
		},
		"postprovision": {
			{Run: "scripts/post.ps1"},
		},
	}

	mockRunner := mockexec.NewMockCommandRunner()
	// pwsh available so PowerShell warning doesn't fire.
	mockRunner.MockToolInPath("pwsh", nil)

	mgr := NewHooksManager(tempDir, mockRunner)
	result := mgr.ValidateHooks(t.Context(), hooksMap)

	// No runtime warnings for Bash/PowerShell hooks.
	for _, w := range result.Warnings {
		require.NotContains(t, w.Message, "Python",
			"shell-only hooks must not trigger runtime warnings")
	}

	require.NoError(t,
		mgr.ValidateRuntimesErr(t.Context(), hooksMap))
}

func Test_ValidateHooks_MixedHooks(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	// Create both shell and Python scripts.
	require.NoError(t,
		os.MkdirAll(filepath.Join(tempDir, "scripts"), osutil.PermissionDirectory))
	require.NoError(t,
		os.WriteFile(
			filepath.Join(tempDir, "scripts", "setup.sh"),
			nil, osutil.PermissionExecutableFile,
		),
	)
	require.NoError(t,
		os.MkdirAll(filepath.Join(tempDir, "hooks"), osutil.PermissionDirectory))
	require.NoError(t,
		os.WriteFile(
			filepath.Join(tempDir, "hooks", "migrate.py"),
			[]byte("print('migrate')"), osutil.PermissionExecutableFile,
		),
	)

	hooksMap := map[string][]*HookConfig{
		"preprovision": {
			{Run: "scripts/setup.sh"},
		},
		"postprovision": {
			{Run: "hooks/migrate.py"},
		},
	}

	mockRunner := mockexec.NewMockCommandRunner()
	// Python NOT available.
	mockRunner.MockToolInPath("py", osexec.ErrNotFound)
	mockRunner.MockToolInPath("python", osexec.ErrNotFound)
	mockRunner.MockToolInPath("python3", osexec.ErrNotFound)
	// pwsh available — no PowerShell warning.
	mockRunner.MockToolInPath("pwsh", nil)

	mgr := NewHooksManager(tempDir, mockRunner)
	result := mgr.ValidateHooks(t.Context(), hooksMap)

	// Exactly one language warning (Python), no shell warnings.
	pythonWarnings := 0
	for _, w := range result.Warnings {
		if strings.Contains(w.Message, "Python") {
			pythonWarnings++
			require.Contains(t, w.Message, "postprovision")
		}
	}
	require.Equal(t, 1, pythonWarnings,
		"expected exactly one Python warning for mixed hooks")

	err := mgr.ValidateRuntimesErr(t.Context(), hooksMap)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Python")
}

func ensureScriptsExist(t *testing.T, configs map[string][]*HookConfig) {
	for _, hooks := range configs {
		for _, hook := range hooks {
			ext := filepath.Ext(hook.Run)

			if isValidFileExtension(ext) {
				err := os.MkdirAll(filepath.Dir(hook.Run), osutil.PermissionDirectory)
				require.NoError(t, err)
				err = os.WriteFile(hook.Run, nil, osutil.PermissionExecutableFile)
				require.NoError(t, err)
			}
		}
	}
}

var fileExtensionRegex = regexp.MustCompile(`^\.[\w]{1,4}$`)

func isValidFileExtension(extension string) bool {
	return strings.ToLower(extension) != "" && fileExtensionRegex.MatchString(extension)
}
