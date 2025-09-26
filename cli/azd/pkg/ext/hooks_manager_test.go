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
		// All hooksMap are invalid because they are missing a script type
		hooksMap := map[string][]*HookConfig{
			"preinit": {
				{
					Run: "echo 'Hello'",
				},
			},
			"postinit": {
				{
					Run: "echo 'Hello'",
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
		// All hooksMap are invalid because they are missing a script type
		hooksMap := map[string][]*HookConfig{
			"preinit": {
				{
					Run: "echo 'Hello'",
				},
			},
			"postinit": {
				{
					Run: "echo 'Hello'",
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
