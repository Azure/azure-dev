// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// envRemoveAction.Run tests
// ---------------------------------------------------------------------------

func newEnvRemoveTestContext(t *testing.T) *azdcontext.AzdContext {
	t.Helper()
	dir := t.TempDir()
	azdCtx := azdcontext.NewAzdContextWithDirectory(dir)
	return azdCtx
}

func setDefaultEnv(t *testing.T, azdCtx *azdcontext.AzdContext, name string) {
	t.Helper()
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: name}))
}

func Test_EnvRemoveAction_NoEnvName(t *testing.T) {
	t.Parallel()
	azdCtx := newEnvRemoveTestContext(t)
	mockCtx := mocks.NewMockContext(context.Background())
	envMgr := &mockenv.MockEnvManager{}
	console := mockinput.NewMockConsole()

	flags := &envRemoveFlags{global: &internal.GlobalCommandOptions{}}
	action := newEnvRemoveAction(azdCtx, envMgr, console, &output.NoneFormatter{}, &bytes.Buffer{}, flags, nil)

	_, err := action.Run(*mockCtx.Context)
	require.Error(t, err)
}

func Test_EnvRemoveAction_EnvNotFound_InList(t *testing.T) {
	azdCtx := newEnvRemoveTestContext(t)
	setDefaultEnv(t, azdCtx, "test-env")

	envMgr := &mockenv.MockEnvManager{}
	envMgr.On("List", mock.Anything).Return([]*environment.Description{}, nil)

	console := mockinput.NewMockConsole()
	flags := &envRemoveFlags{global: &internal.GlobalCommandOptions{}}
	action := newEnvRemoveAction(azdCtx, envMgr, console, &output.NoneFormatter{}, &bytes.Buffer{}, flags, nil)

	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func Test_EnvRemoveAction_ForceDelete(t *testing.T) {
	azdCtx := newEnvRemoveTestContext(t)
	setDefaultEnv(t, azdCtx, "my-env")

	envMgr := &mockenv.MockEnvManager{}
	envMgr.On("List", mock.Anything).Return([]*environment.Description{
		{Name: "my-env"},
	}, nil)
	envMgr.On("Delete", "my-env").Return(nil)

	console := mockinput.NewMockConsole()
	flags := &envRemoveFlags{global: &internal.GlobalCommandOptions{}, force: true}
	action := newEnvRemoveAction(azdCtx, envMgr, console, &output.NoneFormatter{}, &bytes.Buffer{}, flags, nil)

	result, err := action.Run(context.Background())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Message.Header, "was removed")
}

func Test_EnvRemoveAction_DeleteError(t *testing.T) {
	azdCtx := newEnvRemoveTestContext(t)
	setDefaultEnv(t, azdCtx, "my-env")

	envMgr := &mockenv.MockEnvManager{}
	envMgr.On("List", mock.Anything).Return([]*environment.Description{
		{Name: "my-env"},
	}, nil)
	envMgr.On("Delete", "my-env").Return(fmt.Errorf("io error"))

	console := mockinput.NewMockConsole()
	flags := &envRemoveFlags{global: &internal.GlobalCommandOptions{}, force: true}
	action := newEnvRemoveAction(azdCtx, envMgr, console, &output.NoneFormatter{}, &bytes.Buffer{}, flags, nil)

	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "io error")
}

func Test_EnvRemoveAction_WithFlagOverride(t *testing.T) {
	azdCtx := newEnvRemoveTestContext(t)
	// Even without default env, the flag name takes precedence
	envMgr := &mockenv.MockEnvManager{}
	envMgr.On("List", mock.Anything).Return([]*environment.Description{
		{Name: "flag-env"},
	}, nil)
	envMgr.On("Delete", "flag-env").Return(nil)

	console := mockinput.NewMockConsole()
	flags := &envRemoveFlags{global: &internal.GlobalCommandOptions{}, force: true}
	flags.EnvironmentName = "flag-env"
	action := newEnvRemoveAction(azdCtx, envMgr, console, &output.NoneFormatter{}, &bytes.Buffer{}, flags, nil)

	result, err := action.Run(context.Background())
	require.NoError(t, err)
	assert.Contains(t, result.Message.Header, "flag-env")
}

func Test_EnvRemoveAction_ConfirmDenied(t *testing.T) {
	azdCtx := newEnvRemoveTestContext(t)
	setDefaultEnv(t, azdCtx, "my-env")

	envMgr := &mockenv.MockEnvManager{}
	envMgr.On("List", mock.Anything).Return([]*environment.Description{
		{Name: "my-env"},
	}, nil)

	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(false)

	flags := &envRemoveFlags{global: &internal.GlobalCommandOptions{}, force: false}
	action := newEnvRemoveAction(azdCtx, envMgr, console, &output.NoneFormatter{}, &bytes.Buffer{}, flags, nil)

	_, err := action.Run(context.Background())
	// When user declines, the return is (nil, nil)
	require.NoError(t, err)
}

func Test_EnvRemoveAction_ListError(t *testing.T) {
	azdCtx := newEnvRemoveTestContext(t)
	setDefaultEnv(t, azdCtx, "my-env")

	envMgr := &mockenv.MockEnvManager{}
	envMgr.On("List", mock.Anything).Return(([]*environment.Description)(nil), fmt.Errorf("db error"))

	console := mockinput.NewMockConsole()
	flags := &envRemoveFlags{global: &internal.GlobalCommandOptions{}}
	action := newEnvRemoveAction(azdCtx, envMgr, console, &output.NoneFormatter{}, &bytes.Buffer{}, flags, nil)

	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
}

// ---------------------------------------------------------------------------
// processHooks deeper paths — with actual hooks that pass validation
// ---------------------------------------------------------------------------

func Test_ProcessHooks_SkipTrue(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	hooks := []*ext.HookConfig{
		{Run: "echo hello"},
	}
	action := &hooksRunAction{
		console: mockCtx.Console,
		flags:   &hooksRunFlags{},
	}
	// skip=true should skip actual execution
	err := action.processHooks(*mockCtx.Context, "", "prehook", hooks, hookContextProject, true)
	require.NoError(t, err)
}

func Test_ProcessHooks_NilHooks(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	action := &hooksRunAction{
		console: mockCtx.Console,
		flags:   &hooksRunFlags{},
	}
	err := action.processHooks(*mockCtx.Context, "", "prehook", nil, hookContextProject, false)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// validateAndWarnHooks — requires projectConfig + importManager, tested
// indirectly via hooksRunAction construction. Removed direct test as it
// needs heavy dependencies (importManager, commandRunner).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Real HelpFooter functions coverage (not already tested in existing files)
// ---------------------------------------------------------------------------

// These are real functions from templates.go/env.go etc that aren't already covered

func Test_GetCmdTemplateSourceHelpFooter3(t *testing.T) {
	t.Parallel()
	footer := getCmdTemplateSourceHelpFooter(nil)
	assert.NotEmpty(t, footer)
}

func Test_GetCmdEnvConfigHelpFooter3(t *testing.T) {
	t.Parallel()
	footer := getCmdEnvConfigHelpFooter(nil)
	assert.NotEmpty(t, footer)
}

// ---------------------------------------------------------------------------
// newEnvRemoveCmd — Args function coverage
// ---------------------------------------------------------------------------

func Test_NewEnvRemoveCmd_ArgsValidation(t *testing.T) {
	cmd := newEnvRemoveCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "remove <environment>", cmd.Use)

	// Test: zero args should be allowed
	err := cmd.Args(cmd, []string{})
	require.NoError(t, err)

	// Test: one arg should be allowed and set the flag
	cmd.Flags().String(internal.EnvironmentNameFlagName, "", "")
	err = cmd.Args(cmd, []string{"my-env"})
	require.NoError(t, err)
	val, _ := cmd.Flags().GetString(internal.EnvironmentNameFlagName)
	assert.Equal(t, "my-env", val)
}

func Test_NewEnvRemoveCmd_ArgsConflict(t *testing.T) {
	cmd := newEnvRemoveCmd()
	cmd.Flags().String(internal.EnvironmentNameFlagName, "", "")
	_ = cmd.Flags().Set(internal.EnvironmentNameFlagName, "other-env")

	err := cmd.Args(cmd, []string{"my-env"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "may not be used together")
}

func Test_NewEnvRemoveCmd_TooManyArgs(t *testing.T) {
	cmd := newEnvRemoveCmd()
	err := cmd.Args(cmd, []string{"one", "two"})
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// newEnvRemoveFlags
// ---------------------------------------------------------------------------

func Test_NewEnvRemoveFlags(t *testing.T) {
	t.Parallel()
	cmd := newEnvRemoveCmd()
	global := &internal.GlobalCommandOptions{}
	flags := newEnvRemoveFlags(cmd, global)
	require.NotNil(t, flags)
	assert.Equal(t, global, flags.global)
	assert.False(t, flags.force)
}
