// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/templates"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

// setupInitializeEnvTest wires an initAction with a real environment.Manager backed by
// an isolated temp project directory so initializeEnv can be exercised end-to-end without
// network access. The working directory is changed to the project directory so that
// repository.InitEnvFileValues (which reads .env from the CWD) is deterministic.
func setupInitializeEnvTest(
	t *testing.T,
	mockContext *mocks.MockContext,
	flags *initFlags,
) (*initAction, *azdcontext.AzdContext, environment.Manager) {
	t.Helper()

	projectDir := t.TempDir()
	t.Chdir(projectDir)

	azdCtx := azdcontext.NewAzdContextWithDirectory(projectDir)
	envManager := freshEnvManager(t, mockContext, azdCtx)

	action := &initAction{
		console:        mockContext.Console,
		flags:          flags,
		lazyEnvManager: lazy.From(envManager),
	}

	return action, azdCtx, envManager
}

func TestInitializeEnv(t *testing.T) {
	t.Run("FreshCreateWithRequestedName", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		flags := &initFlags{}
		flags.EnvironmentName = "fresh-dev"

		action, azdCtx, _ := setupInitializeEnvTest(t, mockContext, flags)

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "fresh-dev", env.Name())

		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "fresh-dev", defaultEnv)

		require.NotContains(t, strings.Join(mockContext.Console.Output(), "\n"), "Reusing existing environment")
	})

	t.Run("InteractiveReusePromptWhenNoNameRequested", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		flags := &initFlags{}

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)

		var selectMessage string
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "already exists")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			selectMessage = options.Message
			return 0, nil // Reuse
		})

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "existing-dev", env.Name())
		require.Contains(t, selectMessage, "existing-dev")

		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "existing-dev", defaultEnv)

		require.Contains(t, strings.Join(mockContext.Console.Output(), "\n"), "Reusing existing environment")

		envs, err := envManager.List(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, envs, 1)
	})

	t.Run("InteractiveExactMatchReusesWithoutPrompt", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		flags := &initFlags{}
		flags.EnvironmentName = "existing-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)

		// No Select handler is registered: if the reuse/create prompt were shown, the
		// mock console would panic, failing the test. Re-running init with the same -e
		// value must reuse idempotently without prompting (the agent extension scenario).
		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "existing-dev", env.Name())

		require.NotContains(t, strings.Join(mockContext.Console.Output(), "\n"), "already exists")
		require.Contains(t, strings.Join(mockContext.Console.Output(), "\n"), "Reusing existing environment")

		envs, err := envManager.List(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, envs, 1)
	})

	t.Run("InteractiveCreateNew", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		flags := &initFlags{}
		flags.EnvironmentName = "new-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)

		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "already exists")
		}).Respond(1) // Create new

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "new-dev", env.Name())

		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "new-dev", defaultEnv)

		envs, err := envManager.List(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, envs, 2)
	})

	t.Run("InteractiveCreateNewPromptsForName", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		flags := &initFlags{}

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)

		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "already exists")
		}).Respond(1) // Create new
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "environment name")
		}).Respond("prompted-dev")

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "prompted-dev", env.Name())

		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "prompted-dev", defaultEnv)
	})

	t.Run("NonInteractiveReuseOnMatch", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}
		flags.EnvironmentName = "existing-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "existing-dev", env.Name())

		require.Contains(t, strings.Join(mockContext.Console.Output(), "\n"), "Reusing existing environment")
	})

	t.Run("NonInteractiveReuseOnEmptyRequest", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "existing-dev", env.Name())
	})

	t.Run("NonInteractiveDifferentNameErrors", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}
		flags.EnvironmentName = "other-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)

		_, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.Error(t, err)
		var initErr *environment.EnvironmentInitError
		require.ErrorAs(t, err, &initErr)
	})

	t.Run("NonInteractiveStaleDefaultDifferentNameCreates", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}
		flags.EnvironmentName = "real-dev"

		action, azdCtx, _ := setupInitializeEnvTest(t, mockContext, flags)

		// A stale default points at an environment whose folder is gone. An explicitly
		// requested, different name should not be blocked by the unusable default.
		require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "ghost-dev"}))

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "real-dev", env.Name())

		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "real-dev", defaultEnv)
	})

	t.Run("OrphanFolderRecovery", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		flags := &initFlags{}
		flags.EnvironmentName = "orphan-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)

		// Create the environment folder but never record it as the project default,
		// emulating a previous partial init.
		_, err := envManager.Create(*mockContext.Context, environment.Spec{Name: "orphan-dev"})
		require.NoError(t, err)

		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Empty(t, defaultEnv)

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "orphan-dev", env.Name())

		defaultEnv, err = azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "orphan-dev", defaultEnv)
	})

	t.Run("StaleConfigRecovery", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)

		// Record a default environment whose folder does not exist.
		require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "ghost-dev"}))

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "ghost-dev", env.Name())

		// The environment folder is now created and loadable.
		reloaded, err := envManager.Get(*mockContext.Context, "ghost-dev")
		require.NoError(t, err)
		require.Equal(t, "ghost-dev", reloaded.Name())
	})

	t.Run("MetadataNotClobberedOnReuse", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}
		flags.EnvironmentName = "existing-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", func(env *environment.Environment) {
			env.DotenvSet("USER_KEY", "original")
			require.NoError(t, env.Config.Set("user.config", "original"))
		})

		metadata := templates.Metadata{
			Variables: map[string]string{
				"USER_KEY":     "fromTemplate",
				"TEMPLATE_KEY": "templateValue",
			},
			Config: map[string]string{
				"user.config":     "fromTemplate",
				"template.config": "templateValue",
			},
		}

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, metadata)
		require.NoError(t, err)

		dotenv := env.Dotenv()
		require.Equal(t, "original", dotenv["USER_KEY"], "user value must not be clobbered")
		require.Equal(t, "templateValue", dotenv["TEMPLATE_KEY"], "absent template value should be added")

		userConfig, ok := env.Config.Get("user.config")
		require.True(t, ok)
		require.Equal(t, "original", userConfig, "user config must not be clobbered")

		templateConfig, ok := env.Config.Get("template.config")
		require.True(t, ok)
		require.Equal(t, "templateValue", templateConfig, "absent template config should be added")

		// Reload from disk via a fresh manager to confirm the values were persisted.
		reloadManager := freshEnvManager(t, mockContext, azdCtx)
		reloaded, err := reloadManager.Get(*mockContext.Context, "existing-dev")
		require.NoError(t, err)
		require.Equal(t, "original", reloaded.Dotenv()["USER_KEY"])
		require.Equal(t, "templateValue", reloaded.Dotenv()["TEMPLATE_KEY"])
	})

	t.Run("ReuseFillsAbsentLocationButKeepsExistingSubscription", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}
		flags.EnvironmentName = "existing-dev"
		flags.location = "eastus2"
		flags.subscription = "sub-from-flag"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", func(env *environment.Environment) {
			// Subscription already set; location intentionally absent.
			env.DotenvSet(environment.SubscriptionIdEnvVarName, "sub-already-set")
		})

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)

		dotenv := env.Dotenv()
		require.Equal(t, "eastus2", dotenv[environment.LocationEnvVarName], "absent location should be filled from flag")
		require.Equal(t, "sub-already-set", dotenv[environment.SubscriptionIdEnvVarName],
			"existing subscription must not be clobbered by flag")
	})
}

// freshEnvManager builds a new environment.Manager over the same azd context so tests can
// read environment state from disk without hitting the manager's in-memory cache.
func freshEnvManager(
	t *testing.T,
	mockContext *mocks.MockContext,
	azdCtx *azdcontext.AzdContext,
) environment.Manager {
	t.Helper()
	configManager := config.NewFileConfigManager(config.NewManager())
	localDataStore := environment.NewLocalFileDataStore(azdCtx, configManager)
	envManager, err := environment.NewManager(nil, azdCtx, mockContext.Console, localDataStore, nil)
	require.NoError(t, err)
	return envManager
}

// seedDefaultEnv creates an environment, optionally mutates it, saves it, and records it
// as the project default — emulating a prior successful init.
func seedDefaultEnv(
	t *testing.T,
	ctx context.Context,
	azdCtx *azdcontext.AzdContext,
	envManager environment.Manager,
	name string,
	mutate func(env *environment.Environment),
) {
	t.Helper()

	env, err := envManager.Create(ctx, environment.Spec{Name: name})
	require.NoError(t, err)

	if mutate != nil {
		mutate(env)
		require.NoError(t, envManager.Save(ctx, env))
	}

	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: name}))
}
