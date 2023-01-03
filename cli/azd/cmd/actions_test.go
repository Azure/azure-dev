package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// Tests that the command and action can be initialized successfully
func Test_Command_Actions(t *testing.T) {
	resetOsArgs(t)

	chain := []*actions.MiddlewareRegistration{
		{
			Name:     "skip",
			Resolver: middleware.NewSkipMiddleware,
		},
	}

	// Creates the azd root command with a "Skip" middleware that will skip the invocation
	// of the underlying command / actions
	rootCmd := NewRootCmd(true, chain)
	testCommand(t, rootCmd)
}

func testCommand(t *testing.T, cmd *cobra.Command) {
	// Run the command when we find a leaf command
	if len(cmd.Commands()) == 0 {
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			fullCmd := fmt.Sprintf("%s %s", cmd.Parent().CommandPath(), cmd.Use)
			os.Args = strings.Split(fullCmd, " ")
			err := cmd.ExecuteContext(context.Background())
			require.NoError(t, err)
		})
	}

	// Find and run commands for all child commands
	for _, child := range cmd.Commands() {
		testCommand(t, child)
	}
}

// Reset the OS args after all command tests have completed.
func resetOsArgs(t *testing.T) {
	defaultArgs := os.Args

	t.Cleanup(func() {
		os.Args = defaultArgs
	})
}
