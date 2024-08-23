package gitcli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
)

// CreateGitRepo initializes a Git repository with the provided files.
// It sets up the initial commit and configures the user identity for the repository.
func CreateGitRepo(t *testing.T, ctx context.Context, dir string, files map[string]string) {
	// Create directories and write files
	for path, content := range files {
		fullPath := filepath.Join(dir, path)
		err := os.MkdirAll(filepath.Dir(fullPath), osutil.PermissionDirectory)
		require.NoError(t, err)
		err = os.WriteFile(fullPath, []byte(content), osutil.PermissionFile)
		require.NoError(t, err)
	}

	cmdRun := exec.NewCommandRunner(nil)

	// Initialize a Git repository
	_, err := cmdRun.Run(ctx, exec.NewRunArgs("git", "-C", dir, "init"))
	require.NoError(t, err)

	// Set up Git user configuration
	_, err = cmdRun.Run(ctx, exec.NewRunArgs("git", "-C", dir, "config", "user.email", "test@example.com"))
	require.NoError(t, err)
	_, err = cmdRun.Run(ctx, exec.NewRunArgs("git", "-C", dir, "config", "user.name", "Test User"))
	require.NoError(t, err)

	// Add files and create the initial commit
	_, err = cmdRun.Run(ctx, exec.NewRunArgs("git", "-C", dir, "add", "."))
	require.NoError(t, err)
	_, err = cmdRun.Run(ctx, exec.NewRunArgs("git", "-C", dir, "commit", "-m", "Initial commit"))
	require.NoError(t, err)

	// Cleanup: remove the directory after the test completes
	t.Cleanup(func() {
		err := os.RemoveAll(dir)
		require.NoError(t, err)
	})
}
