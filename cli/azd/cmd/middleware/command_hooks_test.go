package middleware

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_CommandHooks_Middleware(t *testing.T) {
	t.Run("WithValidProjectAndMatchingCommand", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext, err := createAzdContext(t)
		require.NoError(t, err)

		envName := "test"
		commandName := "command"

		projectConfig := project.ProjectConfig{
			Name: envName,
			Scripts: map[string]*ext.ScriptConfig{
				"precommand": {
					Script: "echo 'hello'",
					Type:   ext.ScriptTypeBash,
				},
			},
		}

		err = ensureAzdValid(azdContext, envName, &projectConfig)
		require.NoError(t, err)

		nextFn, actionRan := createNextFn()
		hookRan := setupHookMock(mockContext, 0)
		result, err := runMiddleware(mockContext, azdContext, envName, commandName, nextFn)

		require.NotNil(t, result)
		require.NoError(t, err)

		// Hook will run with valid project, env & matching command name
		require.True(t, *hookRan)
		require.True(t, *actionRan)
	})

	t.Run("ValidProjectWithDifferentCommand", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext, err := createAzdContext(t)
		require.NoError(t, err)

		envName := "test"
		commandName := "another command"

		projectConfig := project.ProjectConfig{
			Name: envName,
			Scripts: map[string]*ext.ScriptConfig{
				"precommand": {
					Script: "echo 'hello'",
					Type:   ext.ScriptTypeBash,
				},
			},
		}

		err = ensureAzdValid(azdContext, envName, &projectConfig)
		require.NoError(t, err)

		nextFn, actionRan := createNextFn()
		hookRan := setupHookMock(mockContext, 0)
		result, err := runMiddleware(mockContext, azdContext, envName, commandName, nextFn)

		require.NotNil(t, result)
		require.NoError(t, err)

		// Hook will not run since the running command is different from the registered command
		require.False(t, *hookRan)
		require.True(t, *actionRan)
	})

	t.Run("ValidProjectWithNoHooks", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext, err := createAzdContext(t)
		require.NoError(t, err)

		envName := "test"
		commandName := "another command"

		projectConfig := project.ProjectConfig{
			Name: envName,
		}

		err = ensureAzdValid(azdContext, envName, &projectConfig)
		require.NoError(t, err)

		nextFn, actionRan := createNextFn()
		hookRan := setupHookMock(mockContext, 0)
		result, err := runMiddleware(mockContext, azdContext, envName, commandName, nextFn)

		require.NotNil(t, result)
		require.NoError(t, err)

		// Hook will not run since there aren't any hooks registered
		require.False(t, *hookRan)
		require.True(t, *actionRan)
	})

	t.Run("WithoutEnv", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext, err := createAzdContext(t)
		require.NoError(t, err)

		envName := "test"
		commandName := "command"

		projectConfig := project.ProjectConfig{
			Name: envName,
			Scripts: map[string]*ext.ScriptConfig{
				"precommand": {
					Script: "echo 'hello'",
					Type:   ext.ScriptTypeBash,
				},
			},
		}

		err = ensureAzdProject(azdContext, &projectConfig)
		require.NoError(t, err)

		nextFn, actionRan := createNextFn()
		hookRan := setupHookMock(mockContext, 0)
		result, err := runMiddleware(mockContext, azdContext, envName, commandName, nextFn)

		require.NotNil(t, result)
		require.NoError(t, err)

		// Hook will not run because the project env has not been set
		require.False(t, *hookRan)
		require.True(t, *actionRan)
	})

	t.Run("WithoutProject", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext, err := createAzdContext(t)
		require.NoError(t, err)

		envName := "test"
		commandName := "command"

		nextFn, actionRan := createNextFn()
		hookRan := setupHookMock(mockContext, 0)
		result, err := runMiddleware(mockContext, azdContext, envName, commandName, nextFn)

		require.NotNil(t, result)
		require.NoError(t, err)

		// Hook will not run because azure.yaml project doesn't exist
		require.False(t, *hookRan)
		require.True(t, *actionRan)
	})

	t.Run("PreHookWithError", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		azdContext, err := createAzdContext(t)
		require.NoError(t, err)

		envName := "test"
		commandName := "command"

		projectConfig := project.ProjectConfig{
			Name: envName,
			Scripts: map[string]*ext.ScriptConfig{
				"precommand": {
					Script: "exit 1",
					Type:   ext.ScriptTypeBash,
				},
			},
		}

		err = ensureAzdValid(azdContext, envName, &projectConfig)
		require.NoError(t, err)

		nextFn, actionRan := createNextFn()
		// Set a non-zero exit code to simulate failure
		hookRan := setupHookMock(mockContext, 1)
		result, err := runMiddleware(mockContext, azdContext, envName, commandName, nextFn)

		require.Nil(t, result)
		require.Error(t, err)

		// Hook will run with matching command
		require.True(t, *hookRan)

		// Action will not run because of pre-hook non zero exit code
		require.False(t, *actionRan)
	})
}

func createAzdContext(t *testing.T) (*azdcontext.AzdContext, error) {
	wd, err := os.Getwd()
	require.NoError(t, err)

	tempDir := t.TempDir()
	err = os.Chdir(tempDir)
	require.NoError(t, err)

	t.Cleanup(func() {
		err := os.Chdir(wd)
		require.NoError(t, err)
	})

	azdContext, err := azdcontext.NewAzdContext()
	if err != nil {
		return nil, err
	}

	azdContext.SetProjectDirectory(tempDir)
	return azdContext, nil
}

func createNextFn() (actions.NextFn, *bool) {
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
	azdContext *azdcontext.AzdContext,
	envName string,
	commandName string,
	nextFn actions.NextFn,
) (*actions.ActionResult, error) {
	commandOptions := internal.GlobalCommandOptions{
		EnvironmentName: envName,
		Cwd:             azdContext.ProjectDirectory(),
		NoPrompt:        false,
	}

	actionOptions := actions.ActionOptions{
		Name: commandName,
	}

	middlewareFn := UseCommandHooks(&commandOptions, mockContext.Console, mockContext.CommandRunner)
	result, err := middlewareFn(*mockContext.Context, &actionOptions, nextFn)

	return result, err
}

// Helper functions below

func ensureAzdValid(azdContext *azdcontext.AzdContext, envName string, projectConfig *project.ProjectConfig) error {
	err := ensureAzdEnv(azdContext, envName)
	if err != nil {
		return err
	}

	err = ensureAzdProject(azdContext, projectConfig)
	if err != nil {
		return err
	}

	err = projectConfig.Save(azdContext.ProjectPath())
	if err != nil {
		return err
	}

	return nil
}

func ensureAzdEnv(azdContext *azdcontext.AzdContext, envName string) error {
	err := azdContext.NewEnvironment(envName)
	if err != nil {
		return err
	}

	env := environment.EmptyWithRoot(azdContext.EnvironmentRoot(envName))
	env.SetEnvName(envName)

	err = env.Save()
	if err != nil {
		return err
	}

	return nil
}

func ensureAzdProject(azdContext *azdcontext.AzdContext, projectConfig *project.ProjectConfig) error {
	err := projectConfig.Save(azdContext.ProjectPath())
	if err != nil {
		return err
	}

	return nil
}
