package cli_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// Verifies every command and their corresponding actions can be initialized. Does not test the execution of `action.Run`.
func Test_CommandsAndActions_Initialize(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	// Create a empty azure.yaml to ensure AzdContext can be constructed
	err := os.WriteFile("azure.yaml", []byte("name: test"), osutil.PermissionFile)
	require.NoError(t, err)

	// Create a empty .github/workflows directory to ensure CI provider can be constructed
	ciProviderPath := filepath.Join(".github", "workflows")
	err = os.MkdirAll(ciProviderPath, osutil.PermissionDirectory)
	require.NoError(t, err)

	// set a dummy infra folder for pipeline config
	err = os.MkdirAll("infra", osutil.PermissionDirectory)
	require.NoError(t, err)
	module, err := os.Create(filepath.Join("infra", "main.ext"))
	require.NoError(t, err)
	defer module.Close()

	chain := []*actions.MiddlewareRegistration{
		{
			Name:     "skip",
			Resolver: newSkipMiddleware,
		},
	}

	// Set environment for commands that require environment.
	envName := "envname"
	azdCtx := azdcontext.NewAzdContextWithDirectory(tempDir)
	localDataStore := environment.NewLocalFileDataStore(azdCtx, config.NewFileConfigManager(config.NewManager()))

	require.NoError(t, err)
	err = azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: envName})
	require.NoError(t, err)

	env := environment.New(envName)
	env.SetSubscriptionId(cfg.SubscriptionID)
	env.SetLocation(cfg.Location)
	err = localDataStore.Save(ctx, env, nil)
	require.NoError(t, err)

	// Also requires that the user is logged in. This is automatically done in CI. Locally, `azd auth login` is required.

	// Creates the azd root command with a "Skip" middleware that will skip the invocation
	// of the underlying command / actions
	rootCmd := cmd.NewRootCmd(true, chain, nil)
	testCommand(t, rootCmd, ctx, chain, tempDir)
}

func testCommand(
	t *testing.T,
	testCmd *cobra.Command,
	ctx context.Context,
	chain []*actions.MiddlewareRegistration,
	cwd string) {

	// Run the command when we find a leaf command
	if testCmd.Runnable() {
		t.Run(testCmd.CommandPath(), func(t *testing.T) {
			use := testCmd.Use

			if v, has := testCmd.Annotations["azdtest.use"]; has {
				use = v
			}

			fullCmd := fmt.Sprintf("%s %s", testCmd.Parent().CommandPath(), use)
			args := strings.Split(fullCmd, " ")[1:]
			args = append(args, "--cwd", cwd)
			childCmd := cmd.NewRootCmd(true, chain, nil)
			childCmd.SetArgs(args)
			err := childCmd.ExecuteContext(ctx)
			require.NoError(t, err)
		})
	} else {
		// Find and run commands for all child commands
		for _, child := range testCmd.Commands() {
			testCommand(t, child, ctx, chain, cwd)
		}
	}
}

// SkipMiddleware is used in select testing scenarios where we
// need to skip the invocation of the middleware & action pipeline
// and just return a value
type skipMiddleware struct {
}

// Creates a new Skip Middleware
func newSkipMiddleware() middleware.Middleware {
	return &skipMiddleware{}
}

// Skips the middleware pipeline and returns a nil value
func (r *skipMiddleware) Run(ctx context.Context, next middleware.NextFn) (*actions.ActionResult, error) {
	return nil, nil
}
