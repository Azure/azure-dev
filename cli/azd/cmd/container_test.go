// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/repository"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azd"
	"github.com/azure/azure-dev/cli/azd/pkg/devcenter"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/platform"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/terraform"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

type emptyTemplateSources struct{ templates.SourceManager }
type namedTemplateSource struct{ templates.Source }

func (*emptyTemplateSources) List(context.Context) ([]*templates.SourceConfig, error) {
	return nil, nil
}

func Test_TemplateManager_UsesScopeConsole(t *testing.T) {
	t.Parallel()
	root := ioc.NewNestedContainer(nil)
	registerCommonDependencies(root)
	ioc.RegisterInstance(root, &internal.GlobalCommandOptions{})
	ioc.RegisterInstance(root, &cobra.Command{})
	ioc.RegisterInstance[templates.SourceManager](root, &emptyTemplateSources{})

	first, err := root.NewScope()
	require.NoError(t, err)
	second, err := root.NewScope()
	require.NoError(t, err)
	firstConsole := mockinput.NewMockConsole()
	secondConsole := mockinput.NewMockConsole()
	ioc.RegisterInstance[input.Console](first, firstConsole)
	ioc.RegisterInstance[input.Console](second, secondConsole)

	for _, scope := range []*ioc.NestedContainer{second, first} {
		var manager *templates.TemplateManager
		require.NoError(t, scope.Resolve(&manager))
		_, err := manager.ListTemplates(t.Context(), nil)
		require.NoError(t, err)
	}
	require.Len(t, firstConsole.SpinnerOps(), 2)
	require.Len(t, secondConsole.SpinnerOps(), 2)
}

func Test_TemplateSourceManager_ResolvesNamedSourceInScope(t *testing.T) {
	t.Parallel()
	root := ioc.NewNestedContainer(nil)
	registerCommonDependencies(root)
	ioc.RegisterInstance(root, templates.NewSourceOptions())
	child, err := root.NewScope()
	require.NoError(t, err)
	source := &namedTemplateSource{}
	child.MustRegisterNamedSingleton("custom", func() templates.Source { return source })

	var manager templates.SourceManager
	require.NoError(t, child.Resolve(&manager))
	resolved, err := manager.CreateSource(t.Context(), &templates.SourceConfig{Type: "custom"})
	require.NoError(t, err)
	require.Same(t, source, resolved)
}

func Test_RepositoryInitializer_IsScoped(t *testing.T) {
	t.Parallel()
	root := ioc.NewNestedContainer(nil)
	registerCommonDependencies(root)
	ioc.RegisterInstance(root, (*git.Cli)(nil))
	ioc.RegisterInstance(root, (*dotnet.Cli)(nil))
	ioc.RegisterInstance(root, (*alpha.FeatureManager)(nil))
	ioc.RegisterInstance(root, (*lazy.Lazy[environment.Manager])(nil))

	var initializers []*repository.Initializer
	for range 2 {
		child, err := root.NewScope()
		require.NoError(t, err)
		ioc.RegisterInstance[input.Console](child, mockinput.NewMockConsole())
		var initializer *repository.Initializer
		require.NoError(t, child.Resolve(&initializer))
		require.NotNil(t, initializer)
		initializers = append(initializers, initializer)
	}
	require.NotSame(t, initializers[0], initializers[1])
}

func Test_DevCenterCommandServices_AreScoped(t *testing.T) {
	t.Parallel()
	container := ioc.NewNestedContainer(nil)
	provider := devcenter.NewPlatform(&platform.Config{Type: devcenter.PlatformKindDevCenter})
	require.NoError(t, provider.ConfigureContainer(container))

	for _, serviceType := range []reflect.Type{
		reflect.TypeFor[devcenter.Manager](), reflect.TypeFor[*devcenter.Prompter](),
	} {
		found := false
		for _, registration := range container.Registrations() {
			if registration.ServiceType == serviceType {
				require.Equal(t, ioc.ScopedLifetime, registration.Lifetime)
				found = true
			}
		}
		require.True(t, found, "missing registration for %s", serviceType)
	}
}

func Test_CommandRunner_InteractiveOutputUsesCommandScope(t *testing.T) {
	t.Parallel()
	root := ioc.NewNestedContainer(nil)
	registerCommonDependencies(root)
	ioc.RegisterInstance(root, &internal.GlobalCommandOptions{})
	rootOut := &bytes.Buffer{}
	rootCmd := &cobra.Command{}
	rootCmd.SetOut(rootOut)
	ioc.RegisterInstance(root, rootCmd)

	for range 2 {
		child, err := root.NewScope()
		require.NoError(t, err)
		childOut := &bytes.Buffer{}
		childCmd := &cobra.Command{}
		childCmd.SetOut(childOut)
		ioc.RegisterInstance(child, childCmd)
		var runner exec.CommandRunner
		require.NoError(t, child.Resolve(&runner))
		_, err = runner.Run(t.Context(), exec.NewRunArgs("go", "version").WithInteractive(true))
		require.NoError(t, err)
		require.NotEmpty(t, childOut.String())
	}
	require.Empty(t, rootOut.String())
}

func Test_GitCli_InteractiveErrorUsesCommandScope(t *testing.T) {
	t.Parallel()
	root := ioc.NewNestedContainer(nil)
	registerCommonDependencies(root)
	ioc.RegisterInstance(root, &internal.GlobalCommandOptions{})
	rootErr := &bytes.Buffer{}
	rootCmd := &cobra.Command{}
	rootCmd.SetErr(rootErr)
	ioc.RegisterInstance(root, rootCmd)

	child, err := root.NewScope()
	require.NoError(t, err)
	childErr := &bytes.Buffer{}
	childCmd := &cobra.Command{}
	childCmd.SetErr(childErr)
	ioc.RegisterInstance(child, childCmd)
	var cli *git.Cli
	require.NoError(t, child.Resolve(&cli))
	require.Error(t, cli.PushUpstream(t.Context(), t.TempDir(), "origin", "branch"))
	require.NotEmpty(t, childErr.String())
	require.Empty(t, rootErr.String())
}

func Test_TerraformCli_UsesCommandRunnerInScope(t *testing.T) {
	t.Parallel()
	root := ioc.NewNestedContainer(nil)
	registerCommonDependencies(root)
	require.NoError(t, azd.NewDefaultPlatform().ConfigureContainer(root))
	rootRunner := mockexec.NewMockCommandRunner()
	rootRunner.When(func(exec.RunArgs, string) bool { return true }).Respond(exec.RunResult{Stdout: "root"})
	ioc.RegisterInstance[exec.CommandRunner](root, rootRunner)
	for _, expected := range []string{"first", "second"} {
		child, err := root.NewScope()
		require.NoError(t, err)
		runner := mockexec.NewMockCommandRunner()
		runner.When(func(exec.RunArgs, string) bool { return true }).Respond(exec.RunResult{Stdout: expected})
		ioc.RegisterInstance[exec.CommandRunner](child, runner)
		var cli *terraform.Cli
		require.NoError(t, child.Resolve(&cli))
		output, err := cli.Validate(t.Context(), ".")
		require.NoError(t, err)
		require.Equal(t, expected, output)
	}
}

func Test_DotNetCli_UsesCommandRunnerInScope(t *testing.T) {
	t.Parallel()
	root := ioc.NewNestedContainer(nil)
	registerCommonDependencies(root)
	rootCalls := 0
	rootRunner := mockexec.NewMockCommandRunner()
	rootRunner.When(func(exec.RunArgs, string) bool { return true }).RespondFn(func(exec.RunArgs) (exec.RunResult, error) {
		rootCalls++
		return exec.RunResult{}, nil
	})
	ioc.RegisterInstance[exec.CommandRunner](root, rootRunner)

	for range 2 {
		child, err := root.NewScope()
		require.NoError(t, err)
		childCalls := 0
		childRunner := mockexec.NewMockCommandRunner()
		childRunner.When(func(exec.RunArgs, string) bool { return true }).RespondFn(func(
			exec.RunArgs,
		) (exec.RunResult, error) {
			childCalls++
			return exec.RunResult{}, nil
		})
		ioc.RegisterInstance[exec.CommandRunner](child, childRunner)
		var cli *dotnet.Cli
		require.NoError(t, child.Resolve(&cli))
		require.NoError(t, cli.Restore(t.Context(), "project", nil))
		require.Equal(t, 1, childCalls)
	}
	require.Zero(t, rootCalls)
}

func Test_ErrorHandlerPipeline_UsesScopeEnvironment(t *testing.T) {
	t.Parallel()
	for _, parentFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("parentFirst=%t", parentFirst), func(t *testing.T) {
			t.Parallel()
			root := ioc.NewNestedContainer(nil)
			registerCommonDependencies(root)
			var requestedSubscription string
			ioc.RegisterInstance[account.SubscriptionCredentialProvider](root,
				mockaccount.SubscriptionCredentialProviderFunc(func(
					_ context.Context, subscriptionID string,
				) (azcore.TokenCredential, error) {
					requestedSubscription = subscriptionID
					return nil, assert.AnError
				}))
			ioc.RegisterInstance(root, &arm.ClientOptions{})

			scopes := []*ioc.NestedContainer{root}
			for range 2 {
				child, err := root.NewScope()
				require.NoError(t, err)
				scopes = append(scopes, child)
			}
			locations := []string{"eastus", "westus", "centralus"}
			for index, scope := range scopes {
				env := environment.NewWithValues(fmt.Sprintf("scope-%d", index), map[string]string{
					"AZURE_LOCATION":        locations[index],
					"AZURE_SUBSCRIPTION_ID": fmt.Sprintf("subscription-%d", index),
				})
				ioc.RegisterInstance(scope, lazy.From(env))
			}

			failure := fmt.Errorf("resource type 'Microsoft.Web/staticSites': %w", &azcore.ResponseError{
				ErrorCode: "LocationNotAvailableForResourceType", StatusCode: 400,
			})
			order := []int{1, 2, 1, 0}
			if parentFirst {
				order = append([]int{0}, order...)
			}
			pipelines := make(map[int]*errorhandler.ErrorHandlerPipeline)
			for _, index := range order {
				var pipeline *errorhandler.ErrorHandlerPipeline
				require.NoError(t, scopes[index].Resolve(&pipeline))
				require.NotNil(t, pipeline)
				requestedSubscription = ""
				suggestion := pipeline.Process(t.Context(), failure)
				require.NotNil(t, suggestion)
				require.Contains(t, suggestion.Suggestion, fmt.Sprintf("The current region is '%s'.", locations[index]))
				require.Equal(t, fmt.Sprintf("subscription-%d", index), requestedSubscription)
				require.ErrorIs(t, suggestion.Err, failure)
				require.NotEmpty(t, suggestion.Links)
				if previous, found := pipelines[index]; found {
					require.Same(t, previous, pipeline)
				}
				pipelines[index] = pipeline
			}
			require.NotSame(t, pipelines[0], pipelines[1])
			require.NotSame(t, pipelines[1], pipelines[2])
		})
	}
}

func Test_Lazy_Project_Config_Resolution(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
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
	ctx := t.Context()
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
		ctx := context.WithoutCancel(t.Context())
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
		ctx := context.WithoutCancel(t.Context())
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
		ctx := context.WithoutCancel(t.Context())

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

		err := adapter.ExecuteContext(context.WithoutCancel(t.Context()), []string{"package", "--all"})
		require.NoError(t, err)
		require.True(t, debugEnabled, "global --debug flag should still be parsed on the rebuilt tree")
		require.Empty(t, capturedPositionalArgs,
			"boolean global flag value should not leak into workflow step positional args")
	})

	t.Run("NewRootCmdPreservesMiddlewareChain", func(t *testing.T) {
		// Verify that building a real command tree via NewRootCmd preserves
		// the full middleware chain (debug, ux, telemetry, error, loginGuard, etc.)
		container := ioc.NewNestedContainer(nil)
		ctx := context.WithoutCancel(t.Context())
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
		ctx := context.WithoutCancel(t.Context())

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
		ctx := context.WithoutCancel(t.Context())

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
	ctx := context.WithoutCancel(t.Context())
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

// --- lazyEnvironmentResolver.Getenv Tests ---

func Test_LazyEnvironmentResolver_Getenv_Success(t *testing.T) {
	t.Parallel()

	env := environment.NewWithValues("test", map[string]string{
		"MY_VAR":  "my_value",
		"ANOTHER": "another_value",
	})

	resolver := &lazyEnvironmentResolver{
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return env, nil
		}),
	}

	assert.Equal(t, "my_value", resolver.Getenv("MY_VAR"))
	assert.Equal(t, "another_value", resolver.Getenv("ANOTHER"))
	assert.Equal(t, "", resolver.Getenv("MISSING"))
}

func Test_LazyEnvironmentResolver_Getenv_Error(t *testing.T) {
	t.Parallel()

	resolver := &lazyEnvironmentResolver{
		lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
			return nil, assert.AnError
		}),
	}

	// When the lazy env fails, Getenv returns ""
	assert.Equal(t, "", resolver.Getenv("ANY_KEY"))
}

// --- resolveAction Tests ---

func Test_ResolveAction_NotRegistered(t *testing.T) {
	t.Parallel()

	// Create a real empty nested container
	c := ioc.NewNestedContainer(nil)

	// Attempt to resolve a non-existent action
	_, resolveErr := resolveAction[*buildAction](c, "nonexistent-action")
	// Should error because the action isn't registered
	require.Error(t, resolveErr)
}

// --- registerAction Tests ---

func Test_RegisterAction_DoesNotPanic(t *testing.T) {
	t.Parallel()

	// Create a real empty nested container
	c := ioc.NewNestedContainer(nil)

	// This should not panic - it just registers a resolver
	require.NotPanics(t, func() {
		registerAction[*buildAction](c, "test-action")
	})
}

func Test_EnvironmentNewWithValues(t *testing.T) {
	t.Parallel()
	env := environment.NewWithValues("testenv", map[string]string{"K": "V"})
	require.NotNil(t, env)
	require.Equal(t, "V", env.Getenv("K"))
}

func Test_ResolveAction_WithNilMiddleware(t *testing.T) {
	t.Parallel()
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, &internal.GlobalCommandOptions{})
	_, err := resolveAction[*coverageTestAction](container, "test-action")
	require.Error(t, err) // not registered
}

// Verifies the ExtensionActivator used by env refresh resolves from the IoC container.
func Test_Resolve_ExtensionActivator(t *testing.T) {
	t.Parallel()
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance(container, t.Context())
	ioc.RegisterInstance(container, &internal.GlobalCommandOptions{})
	ioc.RegisterInstance(container, &cobra.Command{})
	registerCommonDependencies(container)

	var activator *middleware.ExtensionActivator
	err := container.Resolve(&activator)
	require.NoError(t, err)
	require.NotNil(t, activator)
}
