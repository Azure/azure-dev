package ext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_GetAllHookConfigs(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	t.Run("With Valid Configuration", func(t *testing.T) {
		hooks := map[string]*HookConfig{
			"preinit": {
				Run: "scripts/preinit.sh",
			},
			"postinit": {
				Run: "scripts/postinit.sh",
			},
		}

		ensureScriptsExist(t, hooks)

		hooksManager := NewHooksManager(tempDir)
		validHooks, err := hooksManager.GetAll(hooks)

		require.Len(t, validHooks, len(hooks))
		require.NoError(t, err)
	})

	t.Run("Finds implicit hooks", func(t *testing.T) {
		err := os.MkdirAll(".azure/hooks", osutil.PermissionDirectory)
		require.NoError(t, err)
		err = os.WriteFile(".azure/hooks/preinit.sh", nil, osutil.PermissionExecutableFile)
		require.NoError(t, err)
		err = os.WriteFile(".azure/hooks/postinit.sh", nil, osutil.PermissionExecutableFile)
		require.NoError(t, err)

		hooksManager := NewHooksManager(tempDir)
		validHooks, err := hooksManager.GetAll(map[string]*HookConfig{})

		require.Len(t, validHooks, 2)
		require.NoError(t, err)
	})

	t.Run("With Invalid Configuration", func(t *testing.T) {
		// All hooks are invalid because they are missing a script type
		hooks := map[string]*HookConfig{
			"preinit": {
				Run: "echo 'Hello'",
			},
			"postinit": {
				Run: "echo 'Hello'",
			},
		}

		ensureScriptsExist(t, hooks)

		hooksManager := NewHooksManager(tempDir)
		validHooks, err := hooksManager.GetAll(hooks)

		require.Nil(t, validHooks)
		require.Error(t, err)
	})
}

func Test_GetByParams(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	t.Run("With Valid Configuration", func(t *testing.T) {
		hooks := map[string]*HookConfig{
			"preinit": {
				Run: "scripts/preinit.sh",
			},
			"postinit": {
				Run: "scripts/postinit.sh",
			},
		}

		ensureScriptsExist(t, hooks)

		hooksManager := NewHooksManager(tempDir)
		validHooks, err := hooksManager.GetByParams(hooks, HookTypePre, "init")

		require.Len(t, validHooks, 1)
		require.Equal(t, hooks["preinit"], validHooks[0])
		require.NoError(t, err)
	})

	t.Run("Finds implicit hooks", func(t *testing.T) {
		err := os.MkdirAll(".azure/hooks", osutil.PermissionDirectory)
		require.NoError(t, err)
		err = os.WriteFile(".azure/hooks/preinit.sh", nil, osutil.PermissionExecutableFile)
		require.NoError(t, err)
		err = os.WriteFile(".azure/hooks/postinit.sh", nil, osutil.PermissionExecutableFile)
		require.NoError(t, err)

		hooksManager := NewHooksManager(tempDir)
		validHooks, err := hooksManager.GetByParams(map[string]*HookConfig{}, HookTypePre, "init")

		require.Len(t, validHooks, 1)
		require.Equal(t, filepath.Join(".azure", "hooks", "preinit.sh"), validHooks[0].path)
		require.NoError(t, err)
	})

	t.Run("With Invalid Configuration", func(t *testing.T) {
		// All hooks are invalid because they are missing a script type
		hooks := map[string]*HookConfig{
			"preinit": {
				Run: "echo 'Hello'",
			},
			"postinit": {
				Run: "echo 'Hello'",
			},
		}

		ensureScriptsExist(t, hooks)

		hooksManager := NewHooksManager(tempDir)
		validHooks, err := hooksManager.GetByParams(hooks, HookTypePre, "init")

		require.Nil(t, validHooks)
		require.Error(t, err)
	})
}

func ensureScriptsExist(t *testing.T, configs map[string]*HookConfig) {
	for _, hook := range configs {
		ext := filepath.Ext(hook.Run)

		if ext != "" {
			err := os.MkdirAll(filepath.Dir(hook.Run), osutil.PermissionDirectory)
			require.NoError(t, err)
			err = os.WriteFile(hook.Run, nil, osutil.PermissionExecutableFile)
			require.NoError(t, err)
		}
	}
}
