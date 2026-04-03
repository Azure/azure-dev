// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bash"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/powershell"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_CommandHooks_Middleware_WithValidProjectAndMatchingCommand(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockContext)
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
	registerHookExecutors(mockContext)
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
	registerHookExecutors(mockContext)
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
	registerHookExecutors(mockContext)
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
	registerHookExecutors(mockContext)
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
	registerHookExecutors(mockContext)
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
	registerHookExecutors(mockContext)
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
			Project:        &projectConfig,
			Service:        serviceConfig,
			ServiceContext: project.NewServiceContext(),
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

func Test_ServiceHooks_ValidationUsesServicePath(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockContext)
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "deploy"}

	projectConfig := project.ProjectConfig{
		Name:     envName,
		Services: map[string]*project.ServiceConfig{},
	}

	hookPath := filepath.Join("scripts", "predeploy.ps1")
	expectedShell := "pwsh"
	scriptContents := "Write-Host 'Hello'\n"
	if runtime.GOOS == "windows" {
		hookPath = filepath.Join("scripts", "predeploy.sh")
		expectedShell = "bash"
		scriptContents = "echo hello\n"
	}

	serviceConfig := &project.ServiceConfig{
		EventDispatcher: ext.NewEventDispatcher[project.ServiceLifecycleEventArgs](project.ServiceEvents...),
		Language:        "ts",
		RelativePath:    "./src/api",
		Host:            "appservice",
		Hooks: map[string][]*ext.HookConfig{
			"predeploy": {
				{
					Run: hookPath,
				},
			},
		},
	}

	projectConfig.Services["api"] = serviceConfig

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	projectConfig.Services["api"].Project = &projectConfig

	serviceHookPath := filepath.Join(serviceConfig.Path(), hookPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(serviceHookPath), 0o755))
	require.NoError(t, os.WriteFile(serviceHookPath, []byte(scriptContents), 0o600))

	mockContext.CommandRunner.MockToolInPath("pwsh", nil)

	var executedShell string
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		executedShell = args.Cmd
		return exec.NewRunResult(0, "", ""), nil
	})

	nextFn := func(ctx context.Context) (*actions.ActionResult, error) {
		err := serviceConfig.Invoke(ctx, project.ServiceEventDeploy, project.ServiceLifecycleEventArgs{
			Project:        &projectConfig,
			Service:        serviceConfig,
			ServiceContext: project.NewServiceContext(),
		}, func() error {
			return nil
		})

		return &actions.ActionResult{}, err
	}

	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NotNil(t, result)
	require.NoError(t, err)
	require.Equal(t, expectedShell, executedShell)
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
	return runMiddlewareWithContext(*mockContext.Context, mockContext, envName, projectConfig, runOptions, nextFn)
}

func runMiddlewareWithContext(
	ctx context.Context,
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

	middleware := NewHooksMiddleware(
		envManager,
		env,
		projectConfig,
		project.NewImportManager(nil),
		mockContext.CommandRunner,
		mockContext.Console,
		runOptions,
		mockContext.Container,
	)

	result, err := middleware.Run(ctx, nextFn)

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

func Test_PowerShellWarning_WithPowerShellHooks(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockContext)
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "command"}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"preprovision": {
				{
					Run:   "Write-Host 'hello'",
					Shell: ext.ShellTypePowershell,
				},
			},
		},
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	nextFn, actionRan := createNextFn()
	setupHookMock(mockContext, 0)

	// Mock toolInPath to simulate pwsh not being available but powershell available
	mockContext.CommandRunner.MockToolInPath("pwsh", osexec.ErrNotFound)
	mockContext.CommandRunner.MockToolInPath("powershell", nil) // powershell is available

	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *actionRan)

	// Check that PowerShell warning was displayed (specifically for PowerShell 5.1)
	consoleOutput := mockContext.Console.Output()
	t.Logf("Console output: %v", consoleOutput)
	foundWarning := false
	for _, message := range consoleOutput {
		if strings.Contains(message, "Your computer only has PowerShell 5.1 (`powershell`) installed") {
			foundWarning = true
			break
		}
	}
	require.True(t, foundWarning, "Expected PowerShell 5.1 warning to be displayed")
}

func Test_PowerShellWarning_WithPs1FileHook(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockContext)
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "command"}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"preprovision": {
				{
					Run:   "script.ps1",            // PowerShell file extension
					Shell: ext.ShellTypePowershell, // Explicitly specify shell to avoid detection issues
				},
			},
		},
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	nextFn, actionRan := createNextFn()
	setupHookMock(mockContext, 0)

	// Mock toolInPath to simulate pwsh not being available
	mockContext.CommandRunner.MockToolInPath("pwsh", osexec.ErrNotFound)

	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *actionRan)

	// Check that PowerShell warning was displayed
	consoleOutput := mockContext.Console.Output()
	foundWarning := false
	for _, message := range consoleOutput {
		if strings.Contains(message, "PowerShell 7 (`pwsh`) commands found in project") {
			foundWarning = true
			break
		}
	}
	require.True(t, foundWarning, "Expected PowerShell warning to be displayed for .ps1 file")
}

func Test_PowerShellWarning_WithoutPowerShellHooks(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockContext)
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
	setupHookMock(mockContext, 0)

	// Mock toolInPath to simulate pwsh not being available

	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *actionRan)

	// Check that no PowerShell warning was displayed
	consoleOutput := mockContext.Console.Output()
	foundWarning := false
	for _, message := range consoleOutput {
		if strings.Contains(message, "PowerShell 7 (`pwsh`) commands found in project") {
			foundWarning = true
			break
		}
	}
	require.False(t, foundWarning, "Expected no PowerShell warning for bash hooks")
}

// Test_CommandHooks_ChildAction_HooksStillFire verifies that command hooks fire even when running
// as a child action (e.g., "provision" step inside "azd up" workflow). PR #7171 changed the
// workflowCmdAdapter to rebuild the command tree, and this test ensures hooks still execute
// for workflow step commands.
func Test_CommandHooks_ChildAction_HooksStillFire(t *testing.T) {
	tests := []struct {
		name        string
		commandPath string
		hookName    string
	}{
		{
			name:        "ProvisionHooksFireInWorkflow",
			commandPath: "azd provision",
			hookName:    "preprovision",
		},
		{
			name:        "DeployHooksFireInWorkflow",
			commandPath: "azd deploy",
			hookName:    "predeploy",
		},
		{
			name:        "PackageHooksFireInWorkflow",
			commandPath: "azd package",
			hookName:    "prepackage",
		},
		{
			name:        "RestoreHooksFireInWorkflow",
			commandPath: "azd restore",
			hookName:    "prerestore",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(context.Background())
			registerHookExecutors(mockContext)
			azdContext := createAzdContext(t)

			envName := "test"
			runOptions := Options{CommandPath: tt.commandPath}

			projectConfig := project.ProjectConfig{
				Name: envName,
				Hooks: map[string][]*ext.HookConfig{
					tt.hookName: {
						{
							Run:   "echo 'hook running'",
							Shell: ext.ShellTypeBash,
						},
					},
				},
			}

			err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
			require.NoError(t, err)

			nextFn, actionRan := createNextFn()
			hookRan := setupHookMock(mockContext, 0)

			// Simulate workflow execution: mark context as child action
			childCtx := WithChildAction(*mockContext.Context)
			result, err := runMiddlewareWithContext(
				childCtx, mockContext, envName, &projectConfig, &runOptions, nextFn,
			)

			require.NotNil(t, result)
			require.NoError(t, err)
			require.True(t, *hookRan, "Hook %q should fire even when running as a child action (workflow step)", tt.hookName)
			require.True(t, *actionRan, "Action should run for child action")
		})
	}
}

// Test_CommandHooks_ChildAction_SkipsValidationOnly verifies that when running as a child action,
// hook validation warnings are suppressed but hooks still execute. This ensures the IsChildAction
// guard in HooksMiddleware.Run() only affects validation, not hook execution itself.
func Test_CommandHooks_ChildAction_SkipsValidationOnly(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockContext)
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "azd provision"}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"preprovision": {
				{
					Run:   "echo 'preprovision hook'",
					Shell: ext.ShellTypeBash,
				},
			},
			"postprovision": {
				{
					Run:   "echo 'postprovision hook'",
					Shell: ext.ShellTypeBash,
				},
			},
		},
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	hookCount := 0
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		hookCount++
		return exec.NewRunResult(0, "", ""), nil
	})

	nextFn, actionRan := createNextFn()

	// Execute as child action (workflow step)
	childCtx := WithChildAction(*mockContext.Context)
	result, err := runMiddlewareWithContext(
		childCtx, mockContext, envName, &projectConfig, &runOptions, nextFn,
	)

	require.NotNil(t, result)
	require.NoError(t, err)
	require.True(t, *actionRan, "Action should run")

	// Both pre and post hooks should fire (2 hooks total)
	require.Equal(t, 2, hookCount,
		"Both preprovision and postprovision hooks should fire for child actions")
}

// Test_CommandHooks_ChildAction_PreHookError_StopsAction verifies that when running as a child
// action, a failing pre-hook still prevents the action from executing (same behavior as direct
// command execution).
func Test_CommandHooks_ChildAction_PreHookError_StopsAction(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockContext)
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "azd provision"}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"preprovision": {
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
	hookRan := setupHookMock(mockContext, 1) // Non-zero exit code

	// Execute as child action (workflow step)
	childCtx := WithChildAction(*mockContext.Context)
	result, err := runMiddlewareWithContext(
		childCtx, mockContext, envName, &projectConfig, &runOptions, nextFn,
	)

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, *hookRan, "Pre-hook should still execute for child actions")
	require.False(t, *actionRan, "Action should NOT run when pre-hook fails")
}

func Test_PowerShellWarning_WithPwshAvailable(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockContext)
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "command"}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"precommand": {
				{
					Run:   "Write-Host 'hello'",
					Shell: ext.ShellTypePowershell,
				},
			},
		},
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	nextFn, actionRan := createNextFn()
	setupHookMock(mockContext, 0)

	// Mock toolInPath to simulate pwsh being available
	mockContext.CommandRunner.MockToolInPath("pwsh", nil)

	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *actionRan)

	// Check that no PowerShell warning was displayed
	consoleOutput := mockContext.Console.Output()
	foundWarning := false
	for _, message := range consoleOutput {
		if strings.Contains(message, "PowerShell 7 (`pwsh`) commands found in project") {
			foundWarning = true
			break
		}
	}
	require.False(t, foundWarning, "Expected no PowerShell warning when pwsh is available")
}

func Test_PowerShellWarning_WithNoPowerShellInstalled(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockContext)
	azdContext := createAzdContext(t)

	envName := "test"
	runOptions := Options{CommandPath: "command"}

	projectConfig := project.ProjectConfig{
		Name: envName,
		Hooks: map[string][]*ext.HookConfig{
			"preprovision": {
				{
					Run:   "Write-Host 'hello'",
					Shell: ext.ShellTypePowershell,
				},
			},
		},
	}

	err := ensureAzdValid(mockContext, azdContext, envName, &projectConfig)
	require.NoError(t, err)

	nextFn, actionRan := createNextFn()
	setupHookMock(mockContext, 0)

	// Mock toolInPath to simulate neither pwsh nor powershell being available
	mockContext.CommandRunner.MockToolInPath("pwsh", osexec.ErrNotFound)
	mockContext.CommandRunner.MockToolInPath("powershell", osexec.ErrNotFound)

	result, err := runMiddleware(mockContext, envName, &projectConfig, &runOptions, nextFn)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, *actionRan)

	// Check that the correct PowerShell warning was displayed (no PowerShell installation detected)
	consoleOutput := mockContext.Console.Output()
	t.Logf("Console output: %v", consoleOutput)
	foundWarning := false
	for _, message := range consoleOutput {
		if strings.Contains(message, "No PowerShell installation detected") {
			foundWarning = true
			break
		}
	}
	require.True(t, foundWarning, "Expected 'No PowerShell installation detected' warning to be displayed")
}

// registerHookExecutors registers all hook executors as named
// transients in the mock container so that IoC resolution works
// in tests.
func registerHookExecutors(mockCtx *mocks.MockContext) {
	mockCtx.Container.MustRegisterNamedTransient(
		string(language.ScriptLanguageBash), bash.NewExecutor,
	)
	mockCtx.Container.MustRegisterNamedTransient(
		string(language.ScriptLanguagePowerShell), powershell.NewExecutor,
	)
	mockCtx.Container.MustRegisterSingleton(python.NewCli)
	mockCtx.Container.MustRegisterNamedTransient(
		string(language.ScriptLanguagePython), language.NewPythonExecutor,
	)
}
