// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/cmd/middleware"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/commandresult"
	"github.com/azure/azure-dev/cli/azd/internal/grpcserver"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	provisioningtest "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/test"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
)

// newOfflineContainer builds the root container the way ExecuteWithAutoInstall does. Unexpected HTTP requests fail
// offline. Like production, the root container has no context.Context: CobraBuilder registers one per command, so
// use runInFakeCommand to resolve anything that needs it.
func newOfflineContainer(t *testing.T) *ioc.NestedContainer {
	t.Helper()
	container, _ := newOfflineRoot(t)
	return container
}

func newOfflineRoot(t *testing.T) (*ioc.NestedContainer, *cobra.Command) {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("NO_COLOR", "1")
	t.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no")
	_ = newTestUserConfigManager(t)
	require.NoError(t, project.Save(t.Context(), &project.ProjectConfig{Name: "test"}, "azure.yaml"))

	container := ioc.NewNestedContainer(nil)

	globalOpts := &internal.GlobalCommandOptions{}
	require.NoError(t, ParseGlobalFlags([]string{"--no-prompt", "-e", "dev"}, globalOpts))
	ioc.RegisterInstance(container, globalOpts)

	command, err := newRootCmdForExecution(container, globalOpts)
	require.NoError(t, err)
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)

	httpClient := mockhttp.NewMockHttpUtil()
	ioc.RegisterInstance[policy.Transporter](container, httpClient)
	ioc.RegisterInstance[auth.HttpClient](container, httpClient)

	return container, command
}

// runInFakeCommand runs fn as the action of a command that only exists in tests. CobraBuilder gives fn the same
// per-command scope a real azd command gets, including its context.Context.
func runInFakeCommand(t *testing.T, container *ioc.NestedContainer, fn func(scope *ioc.NestedContainer)) {
	t.Helper()

	root := actions.NewActionDescriptor("azd", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{Use: "azd"},
	})
	ran := false
	root.Add("fake", &actions.ActionDescriptorOptions{
		Command:          &cobra.Command{Short: "A command that only exists in tests."},
		DisableTelemetry: true,
		OutputFormats:    []output.Format{output.NoneFormat},
		DefaultFormat:    output.NoneFormat,
		ActionResolver: func(scope *ioc.NestedContainer) actions.Action {
			return actions.ActionFunc(func(ctx context.Context) (*actions.ActionResult, error) {
				ran = true
				fn(scope)
				return nil, nil
			})
		},
	})

	var builder *CobraBuilder
	require.NoError(t, container.Resolve(&builder))
	command, err := builder.BuildCommand(root)
	require.NoError(t, err)

	command.SetArgs([]string{"fake"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	require.NoError(t, command.ExecuteContext(t.Context()))
	require.True(t, ran, "the action never ran")
}

// runInOfflineCommand is runInFakeCommand with a "dev" environment already created.
func runInOfflineCommand(
	t *testing.T,
	test func(scope *ioc.NestedContainer, manager environment.Manager, env *environment.Environment),
) {
	t.Helper()

	runInFakeCommand(t, newOfflineContainer(t), func(scope *ioc.NestedContainer) {
		manager, env := createDevEnvironment(t, scope)
		test(scope, manager, env)
	})
}

func createDevEnvironment(t *testing.T, scope *ioc.NestedContainer) (environment.Manager, *environment.Environment) {
	t.Helper()

	var manager environment.Manager
	require.NoError(t, scope.Resolve(&manager))
	_, err := manager.Create(t.Context(), environment.Spec{
		Name: "dev", Subscription: "shared-subscription", Location: "eastus2",
	})
	require.NoError(t, err)
	env, err := manager.Get(t.Context(), "dev")
	require.NoError(t, err)

	return manager, env
}

func Test_ProvisioningRegistrations_ProviderScope(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "Root"
		if nested {
			name = "NestedWorkflow"
		}
		t.Run(name, func(t *testing.T) {
			runInOfflineCommand(t, func(
				root *ioc.NestedContainer, envManager environment.Manager, env *environment.Environment,
			) {
				env.DotenvSet("LAYER_SUBSCRIPTION", "layer-subscription")
				env.DotenvSet("LAYER_LOCATION", "westus2")
				require.NoError(t, envManager.Save(t.Context(), env))

				var providerEnv environment.ScopedEnvironment
				var providerManager environment.Manager
				root.MustRegisterNamedTransient(string(provisioning.Test), func(
					manager environment.Manager,
					scoped environment.ScopedEnvironment,
					console input.Console,
					prompter prompt.Prompter,
				) provisioning.Provider {
					providerEnv, providerManager = scoped, manager
					return provisioningtest.NewTestProvider(manager, scoped, console, prompter)
				})

				container := root
				if nested {
					var err error
					container, err = root.NewScope()
					require.NoError(t, err)
				}
				var manager *provisioning.Manager
				require.NoError(t, container.Resolve(&manager))
				aliases := map[string]string{
					environment.SubscriptionIdEnvVarName: "LAYER_SUBSCRIPTION",
					environment.LocationEnvVarName:       "LAYER_LOCATION",
				}
				require.NoError(t, manager.Initialize(t.Context(), ".", provisioning.Options{
					Provider: provisioning.Test, ParamAliases: aliases, OutputAliases: aliases,
				}))

				require.NotNil(t, providerEnv)
				require.Same(t, env, providerEnv.BackingEnv())
				require.Same(t, envManager, providerManager)
				require.Equal(t, "layer-subscription", providerEnv.GetSubscriptionId())
				require.Equal(t, "westus2", providerEnv.GetLocation())

				var store environment.LocalDataStore
				require.NoError(t, container.Resolve(&store))
				persisted, err := store.Get(t.Context(), env.Name())
				require.NoError(t, err)
				require.Equal(t, "shared-subscription", persisted.GetSubscriptionId())
				require.Equal(t, "eastus2", persisted.GetLocation())
				require.Equal(t, "layer-subscription", persisted.Getenv("LAYER_SUBSCRIPTION"))
				require.Equal(t, "westus2", persisted.Getenv("LAYER_LOCATION"))
			})
		})
	}
}

func Test_ExtensionRegistrations_SharedEnvironment(t *testing.T) {
	runInOfflineCommand(t, func(root *ioc.NestedContainer, envManager environment.Manager, env *environment.Environment) {
		env.DotenvSet("RPC_VALUE", "shared")
		require.NoError(t, envManager.Save(t.Context(), env))

		scope, err := root.NewScope()
		require.NoError(t, err)
		layer := environment.NewWithValues("layer", map[string]string{"RPC_VALUE": "layer"})
		ioc.RegisterInstance[environment.ScopedEnvironment](scope, layer)

		// Resolving the whole server checks the production constructors for every registered RPC service.
		var server *grpcserver.Server
		require.NoError(t, scope.Resolve(&server))
		require.NotNil(t, server)

		var service azdext.EnvironmentServiceServer
		require.NoError(t, scope.Resolve(&service))
		response, err := service.GetValue(t.Context(), &azdext.GetEnvRequest{EnvName: "dev", Key: "RPC_VALUE"})
		require.NoError(t, err)
		require.Equal(t, "shared", response.Value)

		_, err = service.SetValue(t.Context(), &azdext.SetEnvRequest{
			EnvName: "dev", Key: "RPC_VALUE", Value: "from-extension",
		})
		require.NoError(t, err)
		var concrete *environment.Environment
		require.NoError(t, scope.Resolve(&concrete))
		require.Same(t, env, concrete)
		require.Equal(t, "from-extension", concrete.Getenv("RPC_VALUE"))
		require.Equal(t, "layer", layer.Getenv("RPC_VALUE"))

		var store environment.LocalDataStore
		require.NoError(t, scope.Resolve(&store))
		persisted, err := store.Get(t.Context(), "dev")
		require.NoError(t, err)
		require.Equal(t, "from-extension", persisted.Getenv("RPC_VALUE"))

		var rpcValidation azdext.ValidationServiceServer
		var dispatcher provisioning.ValidationCheckDispatcher
		require.NoError(t, scope.Resolve(&rpcValidation))
		require.NoError(t, scope.Resolve(&dispatcher))
		require.Same(t, rpcValidation, dispatcher)
	})
}

// fakeCommandProbe is an action that is not part of azd. It records what the command scope hands to an action.
type fakeCommandProbe struct {
	env     *environment.Environment
	scoped  environment.ScopedEnvironment
	manager *provisioning.Manager
}

func (p *fakeCommandProbe) Run(ctx context.Context) (*actions.ActionResult, error) {
	return nil, nil
}

// Runs a made-up command through CobraBuilder using the real container registrations and global middleware,
// so tests can look at what a command's scope resolves without going through a real azd command.
func Test_FakeCommand_RunsThroughFramework(t *testing.T) {
	container := newOfflineContainer(t)
	runInFakeCommand(t, container, func(scope *ioc.NestedContainer) {
		createDevEnvironment(t, scope)
	})
	probe := &fakeCommandProbe{}

	root := actions.NewActionDescriptor("azd", &actions.ActionDescriptorOptions{
		Command: &cobra.Command{Use: "azd"},
	})
	root.Add("fake", &actions.ActionDescriptorOptions{
		Command:          &cobra.Command{Short: "A command that only exists in tests."},
		DisableTelemetry: true,
		OutputFormats:    []output.Format{output.NoneFormat},
		DefaultFormat:    output.NoneFormat,
		ActionResolver: func(
			e *environment.Environment,
			scoped environment.ScopedEnvironment,
			manager *provisioning.Manager,
		) actions.Action {
			probe.env, probe.scoped, probe.manager = e, scoped, manager
			return probe
		},
	})
	registerGlobalMiddleware(root)

	var builder *CobraBuilder
	require.NoError(t, container.Resolve(&builder))
	command, err := builder.BuildCommand(root)
	require.NoError(t, err)

	command.SetArgs([]string{"fake"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	require.NoError(t, command.ExecuteContext(t.Context()))

	require.NotNil(t, probe.manager, "the action never ran")
	require.Equal(t, "dev", probe.env.Name())
	require.Same(t, probe.env, probe.scoped.BackingEnv())
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

func Test_LocalDataStore_ResolutionAfterInit(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "Root"
		if nested {
			name = "NestedWorkflow"
		}
		t.Run(name, func(t *testing.T) {
			container := ioc.NewNestedContainer(nil)
			ioc.RegisterInstance(container, t.Context())
			registerCommonDependencies(container)
			container.MustRegisterScoped(func() *lazy.Lazy[*azdcontext.AzdContext] {
				return lazy.NewLazy(func() (*azdcontext.AzdContext, error) {
					return nil, azdcontext.ErrNoProject
				})
			})
			if nested {
				var err error
				container, err = container.NewScope()
				require.NoError(t, err)
			}

			var dataStore environment.LocalDataStore
			for range 2 {
				require.ErrorIs(t, container.Resolve(&dataStore), azdcontext.ErrNoProject)
			}

			var lazyContext *lazy.Lazy[*azdcontext.AzdContext]
			require.NoError(t, container.Resolve(&lazyContext))
			azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
			lazyContext.SetValue(azdContext)

			require.NoError(t, container.Resolve(&dataStore))
			require.Equal(t, filepath.Join(azdContext.EnvironmentRoot("test"), environment.DotEnvFileName),
				dataStore.EnvPath(environment.New("test")))
			_, err := dataStore.Get(t.Context(), "test")
			require.ErrorIs(t, err, environment.ErrNotFound)
		})
	}
}

func Test_EnvironmentRegistrations_InitLifecycle(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, firstLookup := range []string{"Context", "Manager", "DataStore"} {
			scopeName := "Root"
			if nested {
				scopeName = "NestedWorkflow"
			}
			t.Run(scopeName+"/"+firstLookup, func(t *testing.T) {
				t.Chdir(t.TempDir())
				root := ioc.NewNestedContainer(nil)
				ioc.RegisterInstance(root, t.Context())
				registerCommonDependencies(root)
				ioc.RegisterInstance(root, newTestUserConfigManager(t))
				console := mocks.NewMockContext(t.Context()).Console
				root.MustRegisterScoped(func() input.Console { return console })

				container := root
				var sibling *ioc.NestedContainer
				if nested {
					var err error
					container, err = root.NewScope()
					require.NoError(t, err)
					sibling, err = root.NewScope()
					require.NoError(t, err)
				}

				var lazyManager *lazy.Lazy[environment.Manager]
				require.NoError(t, container.Resolve(&lazyManager))
				_, err := lazyManager.GetValue()
				require.ErrorIs(t, err, azdcontext.ErrNoProject)
				for range 2 {
					switch firstLookup {
					case "Context":
						var azdContext *azdcontext.AzdContext
						require.ErrorIs(t, container.Resolve(&azdContext), azdcontext.ErrNoProject)
					case "Manager":
						var manager environment.Manager
						require.ErrorIs(t, container.Resolve(&manager), azdcontext.ErrNoProject)
					case "DataStore":
						var dataStore environment.LocalDataStore
						require.ErrorIs(t, container.Resolve(&dataStore), azdcontext.ErrNoProject)
					}
				}

				var lazyContext *lazy.Lazy[*azdcontext.AzdContext]
				require.NoError(t, container.Resolve(&lazyContext))
				azdContext := azdcontext.NewAzdContextWithDirectory(t.TempDir())
				lazyContext.SetValue(azdContext)
				require.NoError(t, project.Save(t.Context(), &project.ProjectConfig{Name: "test"}, azdContext.ProjectPath()))

				manager, err := lazyManager.GetValue()
				require.NoError(t, err)
				env, err := manager.Create(t.Context(), environment.Spec{Name: "dev"})
				require.NoError(t, err)
				env.DotenvSet("LIFECYCLE_TEST", "saved")
				require.NoError(t, manager.Save(t.Context(), env))
				env.DotenvSet("LIFECYCLE_TEST", "unsaved")
				require.NoError(t, manager.Reload(t.Context(), env))
				require.Equal(t, "saved", env.Getenv("LIFECYCLE_TEST"))
				require.Equal(t, filepath.Join(azdContext.EnvironmentRoot("dev"), environment.DotEnvFileName),
					manager.EnvPath(env))

				var resolvedManager environment.Manager
				require.NoError(t, container.Resolve(&resolvedManager))
				require.Same(t, manager, resolvedManager)
				loaded, err := resolvedManager.Get(t.Context(), "dev")
				require.NoError(t, err)
				require.Equal(t, "saved", loaded.Getenv("LIFECYCLE_TEST"))
				cached, err := resolvedManager.Get(t.Context(), "dev")
				require.NoError(t, err)
				require.Same(t, loaded, cached)
				nextScope, err := root.NewScope()
				require.NoError(t, err)
				var nextManager environment.Manager
				require.NoError(t, nextScope.Resolve(&nextManager))
				require.Same(t, manager, nextManager)
				nextEnv, err := nextManager.Get(t.Context(), "dev")
				require.NoError(t, err)
				require.Same(t, loaded, nextEnv)

				if nested {
					for _, untouched := range []*ioc.NestedContainer{root, sibling} {
						var untouchedContext *azdcontext.AzdContext
						require.ErrorIs(t, untouched.Resolve(&untouchedContext), azdcontext.ErrNoProject)
					}
				}
			})
		}
	}
}

// Test_EnvironmentRegistrations_SharedInstancePerScope verifies that concrete environment consumers share one backing
// environment per scope and interface consumers preserve child-scope overrides.
func Test_EnvironmentRegistrations_SharedInstancePerScope(t *testing.T) {
	for _, nested := range []bool{false, true} {
		// Resolving the interface or concrete type first must not create separate instances.
		for _, firstResolution := range []string{"Interface", "Concrete"} {
			scopeName := "Root"
			if nested {
				scopeName = "NestedWorkflow"
			}
			t.Run(scopeName+"/"+firstResolution, func(t *testing.T) {
				projectDir := t.TempDir()
				t.Chdir(projectDir)

				root := ioc.NewNestedContainer(nil)
				ioc.RegisterInstance(root, t.Context())
				registerCommonDependencies(root)
				ioc.RegisterInstance(root, newTestUserConfigManager(t))
				root.MustRegisterScoped(func() internal.EnvFlag {
					return internal.EnvFlag{EnvironmentName: "dev"}
				})
				console := mocks.NewMockContext(t.Context()).Console
				root.MustRegisterScoped(func() input.Console { return console })

				container := root
				if nested {
					var err error
					container, err = root.NewScope()
					require.NoError(t, err)
				}

				// Failed resolutions must remain retryable as the project and environment become available.
				var lazyEnv *lazy.Lazy[*environment.Environment]
				require.NoError(t, container.Resolve(&lazyEnv))
				// Resolving the lazy wrapper succeeds without a project; evaluating it needs azure.yaml.
				value, err := lazyEnv.GetValue()
				require.ErrorIs(t, err, azdcontext.ErrNoProject)
				require.Nil(t, value)

				// make sure resolution works the same, no matter if you Resolve() the concrete type or
				// the interface first.
				var env environment.ScopedEnvironment
				var concrete *environment.Environment
				if firstResolution == "Interface" {
					require.ErrorIs(t, container.Resolve(&env), azdcontext.ErrNoProject)
				} else {
					require.ErrorIs(t, container.Resolve(&concrete), azdcontext.ErrNoProject)
				}

				require.NoError(t, project.Save(
					t.Context(), &project.ProjectConfig{Name: "test"}, filepath.Join(projectDir, "azure.yaml")))
				// Saving azure.yaml fixes the missing project, but does not create the requested "dev" environment.
				// A different error proves the lazy wrapper retried instead of caching ErrNoProject.
				value, err = lazyEnv.GetValue()
				require.ErrorIs(t, err, environment.ErrNotFound)
				require.Nil(t, value)

				var manager environment.Manager
				require.NoError(t, container.Resolve(&manager))

				// now that we've actually created the two environments we can test having the
				// lazy singleton call through to Lazy.GetValue() and succeed...
				_, err = manager.Create(t.Context(), environment.Spec{Name: "dev"})
				require.NoError(t, err)
				_, err = manager.Create(t.Context(), environment.Spec{Name: "prod"})
				require.NoError(t, err)

				resolveEnvironment := func(scope *ioc.NestedContainer, name string) environment.ScopedEnvironment {
					t.Helper()

					var resolved environment.ScopedEnvironment
					var backing *environment.Environment

					if firstResolution == "Interface" {
						require.NoError(t, scope.Resolve(&resolved))
						require.NoError(t, scope.Resolve(&backing))
					} else {
						require.NoError(t, scope.Resolve(&backing))
						require.NoError(t, scope.Resolve(&resolved))
					}

					var scopedLazy *lazy.Lazy[*environment.Environment]
					require.NoError(t, scope.Resolve(&scopedLazy))

					lazyValue, err := scopedLazy.GetValue()
					require.NoError(t, err)
					require.Same(t, backing, resolved.BackingEnv())
					require.Same(t, backing, lazyValue)
					require.Equal(t, name, resolved.Name())

					// In the basic form the environment is the same for all of these, you're just
					// requesting a different veneer/interface over the top of it.
					resolved.DotenvSet("CONSUMER_TEST", name)
					require.Equal(t, name, backing.Getenv("CONSUMER_TEST"))
					require.Equal(t, name, lazyValue.Getenv("CONSUMER_TEST"))

					return resolved
				}

				// Reuse the same container after both failures: no reset or replacement should be necessary.
				env = resolveEnvironment(container, "dev")
				var resolvedLazy *lazy.Lazy[*environment.Environment]
				require.NoError(t, container.Resolve(&resolvedLazy))
				require.Same(t, lazyEnv, resolvedLazy)

				// The original lazy wrapper should also succeed now that it's resolved once.
				value, err = lazyEnv.GetValue()
				require.NoError(t, err)
				require.Same(t, env.BackingEnv(), value)

				// A child scope selects its own environment without changing the parent's cached instance.
				nextScope, err := container.NewScope()
				require.NoError(t, err)
				// swap from our parent context's 'dev' env to 'prod' for nextScope
				ioc.RegisterInstance(nextScope, internal.EnvFlag{EnvironmentName: "prod"})
				nextEnv := resolveEnvironment(nextScope, "prod")

				var nextLazy *lazy.Lazy[*environment.Environment]
				require.NoError(t, nextScope.Resolve(&nextLazy))
				require.NotSame(t, lazyEnv, nextLazy)
				require.NotSame(t, env.BackingEnv(), nextEnv.BackingEnv(), "parent and child purposefully diverge")
				require.Same(t, env.BackingEnv(), resolveEnvironment(container, "dev").BackingEnv())

				value, err = lazyEnv.GetValue()
				require.NoError(t, err)
				require.Equal(t, "dev", value.Name())
				require.Equal(t, "dev", value.Getenv("CONSUMER_TEST"))

				provisionScope, err := container.NewScope()
				require.NoError(t, err)
				layerEnv := environment.NewWithValues("layer", map[string]string{
					"CONSUMER_TEST": "backing",
				})
				overriddenEnv := &getenvOverride{
					ScopedEnvironment: layerEnv,
					key:               "CONSUMER_TEST",
					value:             "override",
				}
				ioc.RegisterInstance[environment.ScopedEnvironment](provisionScope, overriddenEnv)

				var scopedEnv environment.ScopedEnvironment
				require.NoError(t, provisionScope.Resolve(&scopedEnv))
				require.Same(t, layerEnv, scopedEnv.BackingEnv())
				require.Equal(t, "override", scopedEnv.Getenv("CONSUMER_TEST"))
				require.Equal(t, "backing", scopedEnv.BackingEnv().Getenv("CONSUMER_TEST"))

				var sharedEnv *environment.Environment
				require.NoError(t, provisionScope.Resolve(&sharedEnv))
				require.Equal(t, "dev", sharedEnv.Name())
				require.NotEqual(t, "layer", sharedEnv.Getenv("CONSUMER_TEST"))

				require.NoError(t, provisioning.UpdateEnvironment(t.Context(), map[string]provisioning.OutputParameter{
					"OUTPUT_TEST": {Value: "deployed"},
				}, sharedEnv, manager))
				require.Equal(t, "deployed", sharedEnv.Getenv("OUTPUT_TEST"))
				require.Empty(t, scopedEnv.Getenv("OUTPUT_TEST"))
				require.Empty(t, nextEnv.Getenv("OUTPUT_TEST"))

				var sharedLazy *lazy.Lazy[*environment.Environment]
				require.NoError(t, provisionScope.Resolve(&sharedLazy))
				sharedValue, err := sharedLazy.GetValue()
				require.NoError(t, err)
				require.Same(t, sharedEnv, sharedValue)
				require.Equal(t, "dev", resolveEnvironment(container, "dev").Name())
			})
		}
	}
}

type getenvOverride struct {
	environment.ScopedEnvironment
	key   string
	value string
}

func (e *getenvOverride) Getenv(key string) string {
	if key == e.key {
		return e.value
	}

	return e.ScopedEnvironment.Getenv(key)
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
		var commandOrders []uint64

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
						commandOrders = append(
							commandOrders,
							commandresult.FollowUpCommandOrderFromContext(ctx),
						)
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
		ctx := commandresult.WithFollowUpCollector(
			context.WithoutCancel(t.Context()),
			commandresult.NewFollowUpCollector(),
		)

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
		require.Equal(t, []uint64{1, 2, 3}, commandOrders,
			"Workflow steps should receive increasing command orders")
	})

	t.Run("NestedCommandsInheritWorkflowStepOrder", func(t *testing.T) {
		var adapter *workflowCmdAdapter
		var commandOrders []uint64

		newCommand := func() *cobra.Command {
			rootCmd := &cobra.Command{Use: "root"}
			rootCmd.AddCommand(
				&cobra.Command{
					Use: "build",
					RunE: func(cmd *cobra.Command, args []string) error {
						ctx := commandresult.EnsureFollowUpCommandOrder(cmd.Context())
						order := commandresult.FollowUpCommandOrderFromContext(ctx)
						commandOrders = append(
							commandOrders,
							order,
						)
						collector := commandresult.FollowUpCollectorFromContext(ctx)
						collector.Add(commandresult.FollowUp{
							ExtensionID:  "test.extension",
							CommandOrder: order,
							EventName:    "postrestore",
							Text:         "restore",
						})
						collector.Add(commandresult.FollowUp{
							ExtensionID:  "test.extension",
							CommandOrder: order,
							EventName:    "postbuild",
							Text:         "build",
						})
						return adapter.ExecuteContext(ctx, []string{"restore"})
					},
				},
				&cobra.Command{
					Use: "restore",
					RunE: func(cmd *cobra.Command, args []string) error {
						commandOrders = append(
							commandOrders,
							commandresult.FollowUpCommandOrderFromContext(cmd.Context()),
						)
						return nil
					},
				},
				&cobra.Command{
					Use: "deploy",
					RunE: func(cmd *cobra.Command, args []string) error {
						order := commandresult.FollowUpCommandOrderFromContext(cmd.Context())
						commandOrders = append(commandOrders, order)
						commandresult.FollowUpCollectorFromContext(cmd.Context()).Add(
							commandresult.FollowUp{
								ExtensionID:  "test.extension",
								CommandOrder: order,
								EventName:    "postdeploy",
								Text:         "deploy",
							},
						)
						return nil
					},
				},
			)
			return rootCmd
		}

		adapter = &workflowCmdAdapter{newCommand: newCommand}
		collector := commandresult.NewFollowUpCollector()
		ctx := commandresult.WithFollowUpCollector(
			context.WithoutCancel(t.Context()),
			collector,
		)

		require.NoError(t, adapter.ExecuteContext(ctx, []string{"build"}))
		require.Equal(t, "build", collector.Text())

		require.NoError(t, adapter.ExecuteContext(ctx, []string{"deploy"}))
		require.Equal(t, []uint64{1, 1, 2}, commandOrders)
		require.Equal(t, "deploy", collector.Text())
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
