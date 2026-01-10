// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

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
// properly sets fresh contexts on both root and subcommands when reused across
// multiple workflow steps, preventing stale cancelled contexts from affecting
// subsequent executions.
func Test_WorkflowCmdAdapter_ContextPropagation(t *testing.T) {
	t.Run("SubcommandReceivesFreshContext", func(t *testing.T) {
		// Track which contexts were seen by the subcommand
		var receivedContexts []context.Context

		// Create a root command with a subcommand
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

		// Create the adapter
		adapter := &workflowCmdAdapter{cmd: rootCmd}

		// Simulate first workflow step
		ctx1, cancel1 := context.WithCancel(context.Background())
		adapter.SetArgs([]string{"sub"})
		err := adapter.ExecuteContext(ctx1)
		require.NoError(t, err)
		require.Len(t, receivedContexts, 1, "First execution should have received context")

		// Verify first context is not cancelled
		select {
		case <-receivedContexts[0].Done():
			t.Fatal("First context should not be cancelled during execution")
		default:
			// Expected: context is still valid
		}

		// Cancel the first context (simulating workflow step completion)
		cancel1()

		// Verify first context is now cancelled
		select {
		case <-receivedContexts[0].Done():
			// Expected: context is cancelled
		default:
			t.Fatal("First context should be cancelled after cancel1()")
		}

		// Simulate second workflow step with a fresh context
		ctx2 := context.Background()
		adapter.SetArgs([]string{"sub"})
		err = adapter.ExecuteContext(ctx2)
		require.NoError(t, err)
		require.Len(t, receivedContexts, 2, "Second execution should have received context")

		// CRITICAL TEST: Verify second execution received a valid context,
		// not the cancelled context from the first execution
		select {
		case <-receivedContexts[1].Done():
			t.Fatal("2nd execution should have received a fresh valid context, not the cancelled one from 1st execution")
		default:
			// Expected: second context is valid
		}

		// Verify the two contexts are different
		require.NotSame(t, receivedContexts[0], receivedContexts[1],
			"Second execution should receive a different context than the first")
	})

	t.Run("NestedSubcommandReceivesFreshContext", func(t *testing.T) {
		// Track which contexts were seen
		var receivedContexts []context.Context

		// Create a root command with nested subcommands
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

		adapter := &workflowCmdAdapter{cmd: rootCmd}

		// First execution
		ctx1, cancel1 := context.WithCancel(context.Background())
		adapter.SetArgs([]string{"parent", "child"})
		err := adapter.ExecuteContext(ctx1)
		require.NoError(t, err)
		require.Len(t, receivedContexts, 1)

		// Cancel first context
		cancel1()

		// Second execution with fresh context
		ctx2 := context.Background()
		adapter.SetArgs([]string{"parent", "child"})
		err = adapter.ExecuteContext(ctx2)
		require.NoError(t, err)
		require.Len(t, receivedContexts, 2)

		// Verify second execution got a valid context
		select {
		case <-receivedContexts[1].Done():
			t.Fatal("Nested subcommand should have received a fresh valid context in second execution")
		default:
			// Expected: context is valid
		}
	})
}
