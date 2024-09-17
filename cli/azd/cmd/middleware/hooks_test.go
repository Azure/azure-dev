package middleware

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_CommandHooks_Middleware_WithValidProjectAndMatchingCommand(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "command"}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"precommand": {
				{
					Run:   "echo 'hello'",
					Shell: ext.ShellTypeBash,
				},
			},
		},
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	nextFn, actionRan := createNextFn()
	hookRan := setupHookMock(mockContext, 0)
	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NotNil(t, result)
	require.NoError(t, err)

	// Hook will run with valid project, env & matching command name
	require.True(t, *hookRan)
	require.True(t, *actionRan)
}

func Test_CommandHooks_Middleware_ValidProjectWithDifferentCommand(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "another command"}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"precommand": {
				{
					Run:   "echo 'hello'",
					Shell: ext.ShellTypeBash,
				},
			},
		},
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	nextFn, actionRan := createNextFn()
	hookRan := setupHookMock(mockContext, 0)
	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NotNil(t, result)
	require.NoError(t, err)

	// Hook will not run since the running command is different from the registered command
	require.False(t, *hookRan)
	require.True(t, *actionRan)
}

func Test_CommandHooks_Middleware_ValidProjectWithNoHooks(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "another command"}

	projectConfig := project.ProjectConfig{
		Name: envName,
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	nextFn, actionRan := createNextFn()
	hookRan := setupHookMock(mockContext, 0)
	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NotNil(t, result)
	require.NoError(t, err)

	// Hook will not run since there aren't any hooks registered
	require.False(t, *hookRan)
	require.True(t, *actionRan)
}

func Test_CommandHooks_Middleware_PreHookWithError(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "command"}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"precommand": {
				{
					Run:   "exit 1",
					Shell: ext.ShellTypeBash,
				},
			},
		},
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	nextFn, actionRan := createNextFn()
	// Set a non-zero exit code to simulate failure
	hookRan := setupHookMock(mockContext, 1)
	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.Nil(t, result)
	require.Error(t, err)

	// Hook will run with matching command
	require.True(t, *hookRan)

	// Action will not run because of pre-hook non zero exit code
	require.False(t, *actionRan)
}

func Test_CommandHooks_Middleware_PreHookWithErrorAndContinue(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "command"}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"precommand": {
				{
					Run:             "exit 1",
					Shell:           ext.ShellTypeBash,
					ContinueOnError: true,
				},
			},
		},
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	nextFn, actionRan := createNextFn()
	// Set a non-zero exit code to simulate failure
	hookRan := setupHookMock(mockContext, 1)
	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NotNil(t, result)
	require.NoError(t, err)

	// Hook will run with matching command
	require.True(t, *hookRan)

	// Action will still run despite a script error because it has been configured to "ContinueOnError"
	require.True(t, *actionRan)
}

func Test_CommandHooks_Middleware_WithCmdAlias(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "command", Aliases: []string{"alias"}}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"prealias": {
				{
					Run:   "echo 'hello'",
					Shell: ext.ShellTypeBash,
				},
			},
		},
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	nextFn, actionRan := createNextFn()
	hookRan := setupHookMock(mockContext, 0)
	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NotNil(t, result)
	require.NoError(t, err)

	// Hook will run with matching alias command
	require.True(t, *hookRan)
	require.True(t, *actionRan)
}

func Test_ServiceHooks_Registered(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "deploy"}

	projectConfig := project.ProjectConfig{
		Name:     envName,
		Services: map[string]*project.ServiceConfig{},
	}

	serviceConfig := &project.ServiceConfig{
		EventDispatcher: ext.NewEventDispatcher[project.ServiceLifecycleEventArgs](project.ServiceEvents...),
		Language:        "ts",
		RelativePath:    "./src/api",
		Host:            "appservice",
		Hooks: map[string][]*ext.HookConfig{
			"predeploy": {
				{
					Shell: ext.ShellTypeBash,
					Run:   "echo 'Hello'",
				},
			},
		},
	}

	projectConfig.Services["api"] = serviceConfig

	preDeployCount := 0

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "predeploy")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		preDeployCount++
		return exec.NewRunResult(0, "", ""), nil
	})

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	projectConfig.Services["api"].Project = &projectConfig

	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		err := serviceConfig.Invoke(ctx, project.ServiceEventDeploy, project.ServiceLifecycleEventArgs{
			Project: &projectConfig,
			Service: serviceConfig,
		}, func() error {
			return nil
		})

		return &actions.ActionResult{}, err
	}

	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NotNil(t, result)
	require.NoError(t, err)
	require.Equal(t, 1, preDeployCount)
}

func createAzdContext(t *testing.T) *azdcontext.AzdContext {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	return azdcontext.NewAzdContextWithDirectory(tempDir)
}

func createNextFn() (NextFn, *bool) {
	actionRan := false

	nextFn := func(context context.Context) (*actions.ActionResult, error) {
		actionRan = true
		return &actions.ActionResult{
			Message: &actions.ResultMessage{
				Header: "Command Header",
			},
		}, nil
	}

	return nextFn, &actionRan
}

func setupHookMock(mockContext *mocks.MockContext, exitCode int) *bool {
	hookRan := false

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		hookRan = true
		result := exec.NewRunResult(exitCode, "", "")

		if exitCode == 0 {
			return result, nil
		} else {
			return result, errors.New("Error")
		}
	})

	return &hookRan
}

func runMiddleware(
	mockContext *mocks.MockContext,
	envName string,
	projectConfig *project.ProjectConfig,
	runOptions *Options,
	nextFn NextFn,
) (*actions.ActionResult, error) {
	env := environment.NewWithValues(envName, nil)

	// Setup environment mocks for save & reload
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, mock.Anything).Return(nil)
	envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	lazyEnvManager := lazy.NewLazy(func() (environment.Manager, error) {
		return envManager, nil
	})

	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return env, nil
	})

	lazyProjectConfig := lazy.NewLazy(func() (*project.ProjectConfig, error) {
		return projectConfig, nil
	})

	middleware := NewHooksMiddleware(
		lazyEnvManager,
		lazyEnv,
		lazyProjectConfig,
		project.NewImportManager(nil),
		mockContext.CommandRunner,
		mockContext.Console,
		runOptions,
	)

	result, err := middleware.Run(*mockContext.Context, nextFn)

	return result, err
}

// Helper functions below

func ensureAzdValid(
	mockContext *mocks.MockContext,
	azdContext *azdcontext.AzdContext,
	envName string,
	projectConfig *project.ProjectConfig,
) error {
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, mock.Anything).Return(nil)
	envManager.On("Reload", mock.Anything, mock.Anything).Return(nil)

	err := ensureAzdEnv(*mockContext.Context, envManager, envName)
	if err != nil {
		return err
	}

	if err := ensureAzdProject(*mockContext.Context, azdContext, projectConfig); err != nil {
		return err
	}

	return nil
}

func ensureAzdEnv(ctx context.Context, envManager environment.Manager, envName string) error {
	env := environment.New(envName)
	err := envManager.Save(ctx, env)
	if err != nil {
		return err
	}

	return nil
}

func ensureAzdProject(ctx context.Context, azdContext *azdcontext.AzdContext, projectConfig *project.ProjectConfig) error {
	if err := project.Save(ctx, projectConfig, azdContext.ProjectPath()); err != nil {
		return err
	}

	return nil
}
