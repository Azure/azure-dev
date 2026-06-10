// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

		out := strings.Join(mockContext.Console.Output(), "\n")
		require.NotContains(t, out, "Reusing existing environment")
		require.NotContains(t, out, "Switching the default environment")
	})

	t.Run("InteractiveReuseSelectNoNameRequested", func(t *testing.T) {
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

		// The select itself is the acknowledgment; no extra "Reusing" line should print.
		require.NotContains(t, strings.Join(mockContext.Console.Output(), "\n"), "Reusing existing environment")

		envs, err := envManager.List(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, envs, 1)
	})

	t.Run("InteractiveExactMatchConfirmsReuse", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		flags := &initFlags{}
		flags.EnvironmentName = "existing-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)

		var confirmMessage string
		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Reuse the existing environment")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			confirmMessage = options.Message
			return true, nil
		})

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "existing-dev", env.Name())
		require.Contains(t, confirmMessage, "existing-dev")

		// The confirm is the acknowledgment; reusing the current default prints no extra
		// "Reusing" or "Switching" line.
		out := strings.Join(mockContext.Console.Output(), "\n")
		require.NotContains(t, out, "Reusing existing environment")
		require.NotContains(t, out, "Switching the default environment")

		envs, err := envManager.List(*mockContext.Context)
		require.NoError(t, err)
		require.Len(t, envs, 1)
	})

	t.Run("InteractiveExistingNonDefaultConfirmYesReusesAndPromotes", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		flags := &initFlags{}
		flags.EnvironmentName = "other-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)
		// A second, non-default environment that already exists on disk.
		_, err := envManager.Create(*mockContext.Context, environment.Spec{Name: "other-dev"})
		require.NoError(t, err)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Reuse the existing environment")
		}).Respond(true)

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "other-dev", env.Name())

		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "other-dev", defaultEnv)

		// Promoting to a different default surfaces the switch note (and never "Reusing").
		out := strings.Join(mockContext.Console.Output(), "\n")
		require.Contains(t, out, "Switching the default environment")
		require.NotContains(t, out, "Reusing existing environment")
	})

	t.Run("InteractiveExistingNonDefaultConfirmNoCancels", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		flags := &initFlags{}
		flags.EnvironmentName = "other-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)
		_, err := envManager.Create(*mockContext.Context, environment.Spec{Name: "other-dev"})
		require.NoError(t, err)

		mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Reuse the existing environment")
		}).Respond(false)

		_, err = action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.ErrorIs(t, err, errInitEnvCancelled)

		// Nothing mutated: the recorded default is unchanged.
		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "existing-dev", defaultEnv)
	})

	t.Run("NonInteractiveExistingNonDefaultReusesAndPromotes", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}
		flags.EnvironmentName = "other-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)
		_, err := envManager.Create(*mockContext.Context, environment.Spec{Name: "other-dev"})
		require.NoError(t, err)

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "other-dev", env.Name())

		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "other-dev", defaultEnv)

		require.Contains(t, strings.Join(mockContext.Console.Output(), "\n"), "Switching the default environment")
	})

	t.Run("RequestedNewNameCreatesAndPromotesWithoutPrompt", func(t *testing.T) {
		// A non-existent requested name is created and promoted to the default in both
		// non-interactive and interactive modes, without any prompt. No Select/Confirm
		// handler is registered: if a prompt were shown, the mock console would panic.
		for _, noPrompt := range []bool{true, false} {
			t.Run(map[bool]string{true: "NoPrompt", false: "Interactive"}[noPrompt], func(t *testing.T) {
				mockContext := mocks.NewMockContext(t.Context())
				mockContext.Console.SetNoPromptMode(noPrompt)
				flags := &initFlags{}
				flags.EnvironmentName = "new-dev"

				action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
				seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)

				env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
				require.NoError(t, err)
				require.Equal(t, "new-dev", env.Name())

				defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
				require.NoError(t, err)
				require.Equal(t, "new-dev", defaultEnv)

				out := strings.Join(mockContext.Console.Output(), "\n")
				require.Contains(t, out, "Switching the default environment")
				require.NotContains(t, out, "already exists")

				envs, err := envManager.List(*mockContext.Context)
				require.NoError(t, err)
				require.Len(t, envs, 2)
			})
		}
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

		// Creating a new environment must never print "Reusing".
		require.NotContains(t, strings.Join(mockContext.Console.Output(), "\n"), "Reusing existing environment")
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

		out := strings.Join(mockContext.Console.Output(), "\n")
		require.Contains(t, out, "Reusing existing environment")
		require.NotContains(t, out, "Switching the default environment")
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

		// Reusing the recorded default in --no-prompt mode tells the user it was reused.
		require.Contains(t, strings.Join(mockContext.Console.Output(), "\n"), "Reusing existing environment")
	})

	t.Run("NonInteractiveStaleDefaultDifferentNameCreates", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}
		flags.EnvironmentName = "real-dev"

		action, azdCtx, _ := setupInitializeEnvTest(t, mockContext, flags)

		// A stale default points at an environment whose folder is gone. An explicitly
		// requested, different name is created and promoted to the default.
		require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "ghost-dev"}))

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.NoError(t, err)
		require.Equal(t, "real-dev", env.Name())

		defaultEnv, err := azdCtx.GetDefaultEnvironmentName()
		require.NoError(t, err)
		require.Equal(t, "real-dev", defaultEnv)

		require.Contains(t, strings.Join(mockContext.Console.Output(), "\n"), "Switching the default environment")
	})

	t.Run("NonInteractiveInvalidRequestedNameErrors", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}
		flags.EnvironmentName = "invalid name with spaces"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "existing-dev", nil)

		_, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.Error(t, err)
		// Must not report "already initialized" — the real problem is the invalid name.
		var initErr *environment.EnvironmentInitError
		require.False(t, errors.As(err, &initErr), "expected invalid-name error, not EnvironmentInitError")
		require.Contains(t, err.Error(), "invalid")
	})

	t.Run("NonInteractiveCorruptDefaultEnvPropagatesError", func(t *testing.T) {
		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.SetNoPromptMode(true)
		flags := &initFlags{}
		// Request the corrupt environment by name so its load error surfaces.
		flags.EnvironmentName = "corrupt-dev"

		action, azdCtx, envManager := setupInitializeEnvTest(t, mockContext, flags)

		// Create the default env normally so its directory exists.
		seedDefaultEnv(t, *mockContext.Context, azdCtx, envManager, "corrupt-dev", nil)

		// Overwrite config.json with invalid JSON to simulate a real load error.
		configPath := filepath.Join(azdCtx.EnvironmentRoot("corrupt-dev"), environment.ConfigFileName)
		require.NoError(t, os.WriteFile(configPath, []byte("{invalid json"), 0600))

		_, err := action.initializeEnv(*mockContext.Context, azdCtx, templates.Metadata{})
		require.Error(t, err)
		// The I/O error must surface rather than being swallowed.
		var initErr *environment.EnvironmentInitError
		require.False(t, errors.As(err, &initErr), "expected load error, not EnvironmentInitError")
		require.Contains(t, err.Error(), "checking existing environment")
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

	t.Run("ReuseHonorsExplicitFlagsOverExisting", func(t *testing.T) {
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
			// User-edited metadata that must not be clobbered on reuse.
			env.DotenvSet("USER_KEY", "original")
		})

		metadata := templates.Metadata{
			Variables: map[string]string{
				"USER_KEY":     "fromTemplate",
				"TEMPLATE_KEY": "templateValue",
			},
		}

		env, err := action.initializeEnv(*mockContext.Context, azdCtx, metadata)
		require.NoError(t, err)

		dotenv := env.Dotenv()
		require.Equal(t, "eastus2", dotenv[environment.LocationEnvVarName], "absent location should be filled from flag")
		// Explicit flags represent direct user intent and win even over an existing value.
		require.Equal(t, "sub-from-flag", dotenv[environment.SubscriptionIdEnvVarName],
			"explicit subscription flag must win over existing value")
		// Template metadata, by contrast, remains set-if-absent and must not clobber user edits.
		require.Equal(t, "original", dotenv["USER_KEY"], "user-edited metadata must not be clobbered")
		require.Equal(t, "templateValue", dotenv["TEMPLATE_KEY"], "absent template metadata should be added")
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
