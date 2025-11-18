// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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
