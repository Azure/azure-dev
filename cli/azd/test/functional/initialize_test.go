package cli_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// Verifies every command and their corresponding actions can be initialized. Does not test the execution of `action.Run`.
func Test_Initialization(t *testing.T) {
	ctx, cancel := newTestContext(t)
	defer cancel()

	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	// Create a empty azure.yaml to ensure AzdContext can be constructed
	err := os.WriteFile("azure.yaml", nil, osutil.PermissionFile)
	require.NoError(t, err)

	chain := []*actions.MiddlewareRegistration{
		{
			Name:     "skip",
			Resolver: newSkipMiddleware,
		},
	}

	// Set environment for commands that require environment.
	envName := "envname"
	azdCtx := azdcontext.NewAzdContextWithDirectory(tempDir)
	err = azdCtx.NewEnvironment(envName)
	require.NoError(t, err)
	err = azdCtx.SetDefaultEnvironmentName(envName)
	require.NoError(t, err)

	env, _ := environment.GetEnvironment(azdCtx, envName)
	env.SetSubscriptionId(testSubscriptionId)
	env.SetLocation(defaultLocation)
	err = env.Save()
	require.NoError(t, err)

	// Also requires that the user is logged in

	// Creates the azd root command with a "Skip" middleware that will skip the invocation
	// of the underlying command / actions
	rootCmd := cmd.NewRootCmd(true, chain)
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
			fullCmd := fmt.Sprintf("%s %s", testCmd.Parent().CommandPath(), testCmd.Use)
			args := strings.Split(fullCmd, " ")[1:]
			args = append(args, "--cwd", cwd)
			childCmd := cmd.NewRootCmd(true, chain)
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
