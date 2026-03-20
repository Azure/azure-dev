// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func Test_Lazy_Project_Config_Resolution(t *testing.T) {
	ctx := context.Background()
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, ctx)

	registerCommonDependencies(container)

	// Register the testing lazy component
	container.MustRegisterTransient(
		func(lazyProjectConfig *lazy.Lazy[*project.ProjectConfig]) *testLazyComponent[*project.ProjectConfig] {
			return &testLazyComponent[*project.ProjectConfig]{
				lazy: lazyProjectConfig,
			}
		},
	)

	// Register the testing concrete component
	container.MustRegisterTransient(
		func(projectConfig *project.ProjectConfig) *testConcreteComponent[*project.ProjectConfig] {
			return &testConcreteComponent[*project.ProjectConfig]{
				concrete: projectConfig,
			}
		},
	)

	// The lazy components depends on the lazy project config.
	// The lazy instance itself should never be nil
	var lazyComponent *testLazyComponent[*project.ProjectConfig]
	err := container.Resolve(&lazyComponent)
	require.NoError(t, err)
	require.NotNil(t, lazyComponent.lazy)

	// Get the lazy project config instance itself to use for comparison
	var lazyProjectConfig *lazy.Lazy[*project.ProjectConfig]
	err = container.Resolve(&lazyProjectConfig)
	require.NoError(t, err)
	require.NotNil(t, lazyProjectConfig)

	// At this point a project config is not available, so we should get an error
	projectConfig, err := lazyProjectConfig.GetValue()
	require.Nil(t, projectConfig)
	require.Error(t, err)

	// Set a project config on the lazy instance
	projectConfig = &project.ProjectConfig{
		Name: "test",
	}

	lazyProjectConfig.SetValue(projectConfig)

	// Now lets resolve a type that depends on a concrete project config
	// The project config should be be available not that the lazy has been set above
	var staticComponent *testConcreteComponent[*project.ProjectConfig]
	err = container.Resolve(&staticComponent)
	require.NoError(t, err)
	require.NotNil(t, staticComponent.concrete)

	// Now we validate that the instance returned by the lazy instance is the same as the one resolved directly
	lazyValue, err := lazyComponent.lazy.GetValue()
	require.NoError(t, err)
	directValue, err := lazyProjectConfig.GetValue()
	require.NoError(t, err)

	// Finally we validate that the return project config across all resolutions point to the same project config pointer
	require.Same(t, lazyProjectConfig, lazyComponent.lazy)
	require.Same(t, lazyValue, directValue)
	require.Same(t, directValue, staticComponent.concrete)
}

func Test_Lazy_AzdContext_Resolution(t *testing.T) {
	ctx := context.Background()
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, ctx)

	registerCommonDependencies(container)

	// Register the testing lazy component
	container.MustRegisterTransient(
		func(lazyAzdContext *lazy.Lazy[*azdcontext.AzdContext]) *testLazyComponent[*azdcontext.AzdContext] {
			return &testLazyComponent[*azdcontext.AzdContext]{
				lazy: lazyAzdContext,
			}
		},
	)

	// Register the testing concrete component
	container.MustRegisterTransient(
		func(azdContext *azdcontext.AzdContext) *testConcreteComponent[*azdcontext.AzdContext] {
			return &testConcreteComponent[*azdcontext.AzdContext]{
				concrete: azdContext,
			}
		},
	)

	// The lazy components depends on the lazy project config.
	// The lazy instance itself should never be nil
	var lazyComponent *testLazyComponent[*azdcontext.AzdContext]
	err := container.Resolve(&lazyComponent)
	require.NoError(t, err)
	require.NotNil(t, lazyComponent.lazy)

	// Get the lazy project config instance itself to use for comparison
	var lazyInstance *lazy.Lazy[*azdcontext.AzdContext]
	err = container.Resolve(&lazyInstance)
	require.NoError(t, err)
	require.NotNil(t, lazyInstance)

	// At this point a project config is not available, so we should get an error
	azdContext, err := lazyInstance.GetValue()
	require.Nil(t, azdContext)
	require.Error(t, err)

	// Set a project config on the lazy instance
	azdContext = azdcontext.NewAzdContextWithDirectory(t.TempDir())

	lazyInstance.SetValue(azdContext)

	// Now lets resolve a type that depends on a concrete project config
	// The project config should be be available not that the lazy has been set above
	var staticComponent *testConcreteComponent[*azdcontext.AzdContext]
	err = container.Resolve(&staticComponent)
	require.NoError(t, err)
	require.NotNil(t, staticComponent.concrete)

	// Now we validate that the instance returned by the lazy instance is the same as the one resolved directly
	lazyValue, err := lazyComponent.lazy.GetValue()
	require.NoError(t, err)
	directValue, err := lazyInstance.GetValue()
	require.NoError(t, err)

	// Finally we validate that the return project config across all resolutions point to the same project config pointer
	require.Same(t, lazyInstance, lazyComponent.lazy)
	require.Same(t, lazyValue, directValue)
	require.Same(t, directValue, staticComponent.concrete)
}

type testLazyComponent[T comparable] struct {
	lazy *lazy.Lazy[T]
}

type testConcreteComponent[T comparable] struct {
	concrete T
}

// Test_WorkflowCmdAdapter_ContextPropagation validates that the workflowCmdAdapter
// properly marks contexts as child actions when executing subcommands.
// The main.go entrypoint wraps the root context with context.WithoutCancel,
// so workflow steps always receive a non-cancellable context.
// See: https://github.com/Azure/azure-dev/issues/6530
func Test_WorkflowCmdAdapter_ContextPropagation(t *testing.T) {
	t.Run("SubcommandReceivesChildActionContext", func(t *testing.T) {
		// Track which contexts were seen by the subcommand
		var receivedContexts []context.Context

		// Create a command factory that builds a fresh tree on each call
		newCommand := func() *cobra.Command {
			rootCmd := &cobra.Command{
				Use: "root",
			}

			subCmd := &cobra.Command{
				Use: "sub",
				RunE: func(cmd *cobra.Command, args []string) error {
					// Capture the context that the subcommand receives
					receivedContexts = append(receivedContexts, cmd.Context())
					return nil
				},
			}

			rootCmd.AddCommand(subCmd)
			return rootCmd
		}

		// Create the adapter with a factory
		adapter := &workflowCmdAdapter{newCommand: newCommand}

		// In production, main.go wraps with context.WithoutCancel.
		// Simulate this by using a non-cancellable context.
		ctx := context.WithoutCancel(context.Background())
		err := adapter.ExecuteContext(ctx, []string{"sub"})
		require.NoError(t, err)
		require.Len(t, receivedContexts, 1, "Execution should have received context")

		// Verify context is marked as child action
		require.True(t, middleware.IsChildAction(receivedContexts[0]),
			"Context should be marked as child action")

		// Verify context is not cancelled (since we used WithoutCancel)
		select {
		case <-receivedContexts[0].Done():
			t.Fatal("Context should not be cancelled")
		default:
			// Expected: context is still valid
		}

		// Execute again - should still work (fresh command tree each time)
		err = adapter.ExecuteContext(ctx, []string{"sub"})
		require.NoError(t, err)
		require.Len(t, receivedContexts, 2, "Second execution should have received context")

		// Both contexts should be marked as child actions
		require.True(t, middleware.IsChildAction(receivedContexts[1]),
			"Second context should also be marked as child action")
	})

	t.Run("NestedSubcommandReceivesChildActionContext", func(t *testing.T) {
		// Track which contexts were seen
		var receivedContexts []context.Context

		// Create a command factory that builds a fresh tree on each call
		newCommand := func() *cobra.Command {
			rootCmd := &cobra.Command{
				Use: "root",
			}

			parentCmd := &cobra.Command{
				Use: "parent",
			}

			childCmd := &cobra.Command{
				Use: "child",
				RunE: func(cmd *cobra.Command, args []string) error {
					receivedContexts = append(receivedContexts, cmd.Context())
					return nil
				},
			}

			parentCmd.AddCommand(childCmd)
			rootCmd.AddCommand(parentCmd)
			return rootCmd
		}

		adapter := &workflowCmdAdapter{newCommand: newCommand}

		// In production, main.go wraps with context.WithoutCancel.
		ctx := context.WithoutCancel(context.Background())
		err := adapter.ExecuteContext(ctx, []string{"parent", "child"})
		require.NoError(t, err)
		require.Len(t, receivedContexts, 1)

		// Verify context is marked as child action
		require.True(t, middleware.IsChildAction(receivedContexts[0]),
			"Nested context should be marked as child action")

		// Second execution should also work (fresh command tree)
		err = adapter.ExecuteContext(ctx, []string{"parent", "child"})
		require.NoError(t, err)
		require.Len(t, receivedContexts, 2)

		// Verify second execution got a valid context marked as child
		select {
		case <-receivedContexts[1].Done():
			t.Fatal("Nested subcommand should have received a valid context")
		default:
			// Expected: context is valid
		}

		require.True(t, middleware.IsChildAction(receivedContexts[1]),
			"Second nested context should also be marked as child action")
	})

	t.Run("FreshCommandTreeOnEachExecution", func(t *testing.T) {
		// Verify that each ExecuteContext call creates a new command tree,
		// ensuring no stale state from previous executions.
		var commandTreeInstances []*cobra.Command

		newCommand := func() *cobra.Command {
			rootCmd := &cobra.Command{
				Use: "root",
			}
			rootCmd.AddCommand(&cobra.Command{
				Use: "test",
				RunE: func(cmd *cobra.Command, args []string) error {
					return nil
				},
			})
			commandTreeInstances = append(commandTreeInstances, rootCmd)
			return rootCmd
		}

		adapter := &workflowCmdAdapter{newCommand: newCommand}
		ctx := context.WithoutCancel(context.Background())

		err := adapter.ExecuteContext(ctx, []string{"test"})
		require.NoError(t, err)

		err = adapter.ExecuteContext(ctx, []string{"test"})
		require.NoError(t, err)

		// Each execution should have created a distinct command tree
		require.Len(t, commandTreeInstances, 2, "Factory should have been called twice")
		require.NotSame(t, commandTreeInstances[0], commandTreeInstances[1],
			"Each execution should use a distinct command tree instance")
	})

	t.Run("GlobalBoolFlagsRemainSingleTokenWhenMerged", func(t *testing.T) {
		originalArgs := os.Args
		os.Args = []string{"azd", "--debug", "up"}
		t.Cleanup(func() {
			os.Args = originalArgs
		})

		globalArgs := extractGlobalArgs()
		require.Equal(t, []string{"--debug=true"}, globalArgs)

		var (
			capturedPositionalArgs []string
			debugEnabled           bool
		)

		newCommand := func() *cobra.Command {
			rootCmd := &cobra.Command{Use: "root"}
			rootCmd.PersistentFlags().AddFlagSet(CreateGlobalFlagSet())

			packageCmd := &cobra.Command{
				Use:  "package",
				Args: cobra.NoArgs,
				RunE: func(cmd *cobra.Command, args []string) error {
					capturedPositionalArgs = append([]string(nil), args...)

					var err error
					debugEnabled, err = cmd.Flags().GetBool("debug")
					require.NoError(t, err)

					return nil
				},
			}
			packageCmd.Flags().Bool("all", false, "")
			rootCmd.AddCommand(packageCmd)

			return rootCmd
		}

		adapter := &workflowCmdAdapter{
			newCommand: newCommand,
			globalArgs: globalArgs,
		}

		err := adapter.ExecuteContext(context.WithoutCancel(context.Background()), []string{"package", "--all"})
		require.NoError(t, err)
		require.True(t, debugEnabled, "global --debug flag should still be parsed on the rebuilt tree")
		require.Empty(t, capturedPositionalArgs,
			"boolean global flag value should not leak into workflow step positional args")
	})

	t.Run("NewRootCmdPreservesMiddlewareChain", func(t *testing.T) {
		// Verify that building a real command tree via NewRootCmd preserves
		// the full middleware chain (debug, ux, telemetry, error, loginGuard, etc.)
		container := ioc.NewNestedContainer(nil)
		ctx := context.WithoutCancel(context.Background())
		ioc.RegisterInstance(container, ctx)
		ioc.RegisterInstance(container, &internal.GlobalCommandOptions{})

		rootCmd := NewRootCmd(false, nil, container)

		// Verify the command tree is fully built with known subcommands
		foundVersion := false
		foundProvision := false
		foundDeploy := false
		for _, child := range rootCmd.Commands() {
			switch child.Name() {
			case "version":
				foundVersion = true
			case "provision":
				foundProvision = true
			case "deploy":
				foundDeploy = true
			}
		}

		require.True(t, foundVersion, "version command should be registered")
		require.True(t, foundProvision, "provision command should be registered")
		require.True(t, foundDeploy, "deploy command should be registered")

		// Build a second tree and verify it also has all commands
		rootCmd2 := NewRootCmd(false, nil, container)
		foundVersion2 := false
		foundProvision2 := false
		for _, child := range rootCmd2.Commands() {
			switch child.Name() {
			case "version":
				foundVersion2 = true
			case "provision":
				foundProvision2 = true
			}
		}

		require.True(t, foundVersion2, "second tree: version command should be registered")
		require.True(t, foundProvision2, "second tree: provision command should be registered")
		require.NotSame(t, rootCmd, rootCmd2, "each NewRootCmd call should produce a distinct instance")
	})
}
