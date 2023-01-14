package ext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_GetAllScriptConfigs(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	t.Run("With Valid Configuration", func(t *testing.T) {
		scripts := map[string]*ScriptConfig{
			"preinit": {
				Path: "scripts/preinit.sh",
			},
			"postinit": {
				Path: "scripts/postinit.sh",
			},
		}

		ensureScriptsExist(t, scripts)

		hooksManager := NewHooksManager(tempDir)
		validScripts, err := hooksManager.GetAllScriptConfigs(scripts)

		require.Len(t, validScripts, len(scripts))
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
		validScripts, err := hooksManager.GetAllScriptConfigs(map[string]*ScriptConfig{})

		require.Len(t, validScripts, 2)
		require.NoError(t, err)
	})

	t.Run("With Invalid Configuration", func(t *testing.T) {
		// All scripts are invalid because they are missing a script type
		scripts := map[string]*ScriptConfig{
			"preinit": {
				Script: "echo 'Hello'",
			},
			"postinit": {
				Script: "echo 'Hello'",
			},
		}

		ensureScriptsExist(t, scripts)

		hooksManager := NewHooksManager(tempDir)
		validScripts, err := hooksManager.GetAllScriptConfigs(scripts)

		require.Nil(t, validScripts)
		require.Error(t, err)
	})
}

func Test_GetScriptConfigsForHook(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	t.Run("With Valid Configuration", func(t *testing.T) {
		scripts := map[string]*ScriptConfig{
			"preinit": {
				Path: "scripts/preinit.sh",
			},
			"postinit": {
				Path: "scripts/postinit.sh",
			},
		}

		ensureScriptsExist(t, scripts)

		hooksManager := NewHooksManager(tempDir)
		validScripts, err := hooksManager.GetScriptConfigsForHook(scripts, HookTypePre, "init")

		require.Len(t, validScripts, 1)
		require.Equal(t, scripts["preinit"], validScripts[0])
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
		validScripts, err := hooksManager.GetScriptConfigsForHook(map[string]*ScriptConfig{}, HookTypePre, "init")

		require.Len(t, validScripts, 1)
		require.Equal(t, filepath.Join(".azure", "hooks", "preinit.sh"), validScripts[0].Path)
		require.NoError(t, err)
	})

	t.Run("With Invalid Configuration", func(t *testing.T) {
		// All scripts are invalid because they are missing a script type
		scripts := map[string]*ScriptConfig{
			"preinit": {
				Script: "echo 'Hello'",
			},
			"postinit": {
				Script: "echo 'Hello'",
			},
		}

		ensureScriptsExist(t, scripts)

		hooksManager := NewHooksManager(tempDir)
		validScripts, err := hooksManager.GetScriptConfigsForHook(scripts, HookTypePre, "init")

		require.Nil(t, validScripts)
		require.Error(t, err)
	})
}

func ensureScriptsExist(t *testing.T, scripts map[string]*ScriptConfig) {
	for _, scriptConfig := range scripts {
		if scriptConfig.Path != "" {
			err := os.MkdirAll(filepath.Dir(scriptConfig.Path), osutil.PermissionDirectory)
			require.NoError(t, err)
			err = os.WriteFile(scriptConfig.Path, nil, osutil.PermissionExecutableFile)
			require.NoError(t, err)
		}
	}
}
