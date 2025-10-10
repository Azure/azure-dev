// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
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
					Shell: ShellTypeBash,
					// Run is missing - this should cause an error
				},
			},
			"postinit": {
				{
					Shell: ShellTypeBash,
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
					Shell: ShellTypeBash,
					// Run is missing - this should cause an error
				},
			},
			"postinit": {
				{
					Shell: ShellTypeBash,
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
		expectedShell    ShellType
		expectingDefault bool
	}{
		{
			name: "No shell specified - should use OS default",
			hookConfig: &HookConfig{
				Name: "test",
				Run:  "echo 'hello'",
			},
			expectedShell:    getDefaultShellForOS(),
			expectingDefault: true,
		},
		{
			name: "Shell explicitly specified - should not use default",
			hookConfig: &HookConfig{
				Name:  "test",
				Shell: ShellTypeBash,
				Run:   "echo 'hello'",
			},
			expectedShell:    ShellTypeBash,
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

			require.Equal(t, tt.expectedShell, config.Shell)
			require.Equal(t, tt.expectingDefault, config.IsUsingDefaultShell())
		})
	}
}

func Test_HooksManager_ValidateDefaultShellWarning(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	hooksManager := NewHooksManager(tempDir, mockContext.CommandRunner)

	// Create hooks without explicit shell configuration
	// DON'T pre-validate - let ValidateHooks do the validation and warning detection
	hooksWithoutShell := map[string][]*HookConfig{
		"prebuild": {
			{
				Name: "prebuild",
				Run:  "echo 'Building...'",
				// No Shell specified - should trigger default shell warning
				// No cwd specified - ValidateHooks should set it
			},
		},
	}

	// ValidateHooks should validate the hooks internally and detect default shell usage
	result := hooksManager.ValidateHooks(context.Background(), hooksWithoutShell)

	// Should have at least one warning about default shell usage
	hasDefaultShellWarning := false
	for _, warning := range result.Warnings {
		if strings.Contains(warning.Message, "Hook configurations found without explicit shell specification") {
			hasDefaultShellWarning = true
			break
		}
	}

	require.True(t, hasDefaultShellWarning, "Expected warning about default shell usage")

	// Also verify that the hook was actually validated and has the default shell set
	hook := hooksWithoutShell["prebuild"][0]
	require.True(t, hook.IsUsingDefaultShell(), "Hook should be marked as using default shell")
	expectedShell := getDefaultShellForOS()
	require.Equal(t, expectedShell, hook.Shell, "Hook should have the OS default shell")
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
