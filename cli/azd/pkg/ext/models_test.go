package ext

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
)

func Test_HooksFromFolderPath(t *testing.T) {
	t.Run("HooksFileExistsYaml", func(t *testing.T) {
		tempDir := t.TempDir()
		hooksPath := filepath.Join(tempDir, "azd.hooks.yaml")
		hooksContent := []byte(`
pre-build:
  shell: sh
  run: ./pre-build.sh
post-build:
  shell: pwsh
  run: ./post-build.ps1
`)

		err := os.WriteFile(hooksPath, hooksContent, osutil.PermissionDirectory)
		require.NoError(t, err)

		expectedHooks := map[string]*HookConfig{
			"pre-build": {
				validated:       false,
				cwd:             "",
				Name:            "",
				Shell:           ShellTypeBash,
				Run:             "./pre-build.sh",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			},
			"post-build": {
				validated:       false,
				cwd:             "",
				Name:            "",
				Shell:           ShellTypePowershell,
				Run:             "./post-build.ps1",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			},
		}

		hooks, err := HooksFromFolderPath(tempDir)
		require.NoError(t, err)
		require.Equal(t, expectedHooks, hooks)
	})

	t.Run("HooksFileExistsYml", func(t *testing.T) {
		tempDir := t.TempDir()
		hooksPath := filepath.Join(tempDir, "azd.hooks.yml")
		hooksContent := []byte(`
pre-build:
  shell: sh
  run: ./pre-build.sh
post-build:
  shell: pwsh
  run: ./post-build.ps1
`)

		err := os.WriteFile(hooksPath, hooksContent, osutil.PermissionDirectory)
		require.NoError(t, err)

		expectedHooks := map[string]*HookConfig{
			"pre-build": {
				validated:       false,
				cwd:             "",
				Name:            "",
				Shell:           ShellTypeBash,
				Run:             "./pre-build.sh",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			},
			"post-build": {
				validated:       false,
				cwd:             "",
				Name:            "",
				Shell:           ShellTypePowershell,
				Run:             "./post-build.ps1",
				ContinueOnError: false,
				Interactive:     false,
				Windows:         nil,
				Posix:           nil,
			},
		}

		hooks, err := HooksFromFolderPath(tempDir)
		require.NoError(t, err)
		require.Equal(t, expectedHooks, hooks)
	})

	t.Run("NoHooksFile", func(t *testing.T) {
		tempDir := t.TempDir()
		hooks, err := HooksFromFolderPath(tempDir)
		require.NoError(t, err)
		var expectedHooks map[string]*HookConfig
		require.Equal(t, expectedHooks, hooks)
	})
}
