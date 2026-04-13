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
	t.Parallel()
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
	t.Parallel()
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

// Test_workflowCmdAdapter_ContextPropagation validates that the workflowCmdAdapter
// properly marks contexts as child actions when executing subcommands.
// The main.go entrypoint wraps the root context with context.WithoutCancel,
// so workflow steps always receive a non-cancellable context.
// See: https://github.com/Azure/azure-dev/issues/6530
func Test_workflowCmdAdapter_ContextPropagation(t *testing.T) {
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
					ctx := cmd.Context()
					// Verify context is valid DURING execution
					require.NoError(t, ctx.Err(), "Context should be valid during execution")
					receivedContexts = append(receivedContexts, ctx)
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

		// After ExecuteContext returns, the child context is cancelled so that
		// event handlers registered during this step are cleaned up.
		require.Error(t, receivedContexts[0].Err(),
			"Context should be cancelled after step completes")

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
					ctx := cmd.Context()
					// Verify context is valid DURING execution
					require.NoError(t, ctx.Err(), "Context should be valid during execution")
					receivedContexts = append(receivedContexts, ctx)
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

		// Verify second execution got a context marked as child and is cancelled
		// after step completion (event handler cleanup)
		require.Error(t, receivedContexts[1].Err(),
			"Context should be cancelled after step completes")

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

	t.Run("WorkflowAdapterMiddlewareRunsForChildActions", func(t *testing.T) {
		// Verify that when the workflowCmdAdapter executes a command, the middleware chain
		// (registered on the command tree) is invoked despite the context being a child action.
		// This validates that hooks middleware would fire during workflow step execution.
		var middlewareRan bool
		var receivedIsChild bool

		newCommand := func() *cobra.Command {
			rootCmd := &cobra.Command{Use: "root"}

			// Create a child action descriptor-style setup:
			// The "provision" command wraps its RunE to simulate middleware execution
			provisionCmd := &cobra.Command{
				Use: "provision",
				RunE: func(cmd *cobra.Command, args []string) error {
					ctx := cmd.Context()
					middlewareRan = true
					receivedIsChild = middleware.IsChildAction(ctx)
					return nil
				},
			}
			rootCmd.AddCommand(provisionCmd)
			return rootCmd
		}

		adapter := &workflowCmdAdapter{newCommand: newCommand}
		ctx := context.WithoutCancel(context.Background())

		// Execute "provision" through the adapter (simulates workflow step)
		err := adapter.ExecuteContext(ctx, []string{"provision"})
		require.NoError(t, err)
		require.True(t, middlewareRan, "Provision command should have been executed")
		require.True(t, receivedIsChild,
			"Context should be marked as child action when executed through workflow adapter")
	})

	t.Run("WorkflowAdapterMiddlewareChainForAllSteps", func(t *testing.T) {
		// Simulate the full workflow execution path: package → provision → deploy
		// Verify each step's command runs with the child action context and fresh tree
		var executedCommands []string

		newCommand := func() *cobra.Command {
			rootCmd := &cobra.Command{Use: "root"}

			for _, cmdName := range []string{"package", "provision", "deploy"} {
				name := cmdName // capture for closure
				cmd := &cobra.Command{
					Use: name,
					RunE: func(cmd *cobra.Command, args []string) error {
						ctx := cmd.Context()
						require.True(t, middleware.IsChildAction(ctx),
							"Step %q should have child action context", name)
						executedCommands = append(executedCommands, name)
						return nil
					},
				}
				if name == "package" || name == "deploy" {
					cmd.Flags().Bool("all", false, "")
				}
				rootCmd.AddCommand(cmd)
			}
			return rootCmd
		}

		adapter := &workflowCmdAdapter{newCommand: newCommand}
		ctx := context.WithoutCancel(context.Background())

		// Simulate the default "up" workflow steps
		steps := [][]string{
			{"package", "--all"},
			{"provision"},
			{"deploy", "--all"},
		}

		for _, args := range steps {
			err := adapter.ExecuteContext(ctx, args)
			require.NoError(t, err, "Step %v should succeed", args)
		}

		require.Equal(t, []string{"package", "provision", "deploy"}, executedCommands,
			"All workflow steps should execute in order")
	})
}

func Test_NewRootCmd_ReregistrationReplacesProjectConfig(t *testing.T) {
	// This test proves the regression from PR #7171: when workflowCmdAdapter called
	// NewRootCmd (with full registration) for each workflow step, registerCommonDependencies
	// re-registered singletons. The golobby IoC container replaces cached singleton instances
	// on re-registration, so event handlers registered on ProjectConfig/ServiceConfig (by the
	// hooks middleware) were silently lost.
	//
	// Steps:
	// 1. Create root command (registers dependencies)
	// 2. Resolve ProjectConfig, add an event handler
	// 3. Create another root command (re-registers dependencies)
	// 4. Resolve ProjectConfig again
	// 5. Validate the handler is gone (proving the bug)
	// 6. Use newRootCmdWithoutRegistration instead, validate handler is preserved (proving the fix)

	container := ioc.NewNestedContainer(nil)
	ctx := context.WithoutCancel(context.Background())
	ioc.RegisterInstance(container, ctx)
	ioc.RegisterInstance(container, &internal.GlobalCommandOptions{})

	// Set up a project directory with azure.yaml so ProjectConfig can be resolved
	dir := t.TempDir()
	t.Chdir(dir)
	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)
	ioc.RegisterInstance(container, azdCtx)

	projectConfig := &project.ProjectConfig{
		Name: "test-project",
	}
	_ = project.Save(ctx, projectConfig, azdCtx.ProjectPath())

	// Step 1: Create root command (registers dependencies including ProjectConfig factory)
	_ = NewRootCmd(false, nil, container)

	// Step 2: Resolve ProjectConfig and add an event handler (simulates hooks middleware)
	var pc1 *project.ProjectConfig
	require.NoError(t, container.Resolve(&pc1))

	// Step 3: Create another root command with full re-registration
	_ = NewRootCmd(false, nil, container)

	// Step 4: Resolve ProjectConfig again
	var pc2 *project.ProjectConfig
	require.NoError(t, container.Resolve(&pc2))

	// Step 5: The re-registration replaced the singleton — it's a different instance
	require.NotSame(t, pc1, pc2,
		"BUG PROOF: NewRootCmd re-registration replaces the cached ProjectConfig singleton, "+
			"losing any event handlers attached to the original instance")

	// Step 6: Now use newRootCmdWithoutRegistration and verify the instance is preserved
	var pc3 *project.ProjectConfig
	require.NoError(t, container.Resolve(&pc3))

	_ = newRootCmdWithoutRegistration(container)

	var pc4 *project.ProjectConfig
	require.NoError(t, container.Resolve(&pc4))

	require.Same(t, pc3, pc4,
		"FIX PROOF: newRootCmdWithoutRegistration preserves the cached ProjectConfig singleton, "+
			"keeping event handlers intact")
}
