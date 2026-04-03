// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestAzdContext(t *testing.T) *azdcontext.AzdContext {
	t.Helper()
	dir := t.TempDir()
	// Create the azure.yaml to make it a valid azd context root
	err := os.WriteFile(filepath.Join(dir, azdcontext.ProjectFileName), []byte("name: test\n"), 0600)
	require.NoError(t, err)
	return azdcontext.NewAzdContextWithDirectory(dir)
}

func newTestEnvManager() *mockenv.MockEnvManager {
	mgr := &mockenv.MockEnvManager{}
	return mgr
}

// --- envSetAction Tests ---

func Test_EnvSetAction_EmptyArgs(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("test", map[string]string{})
	mgr := newTestEnvManager()

	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), &envSetFlags{}, nil)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no environment values provided")
}

func Test_EnvSetAction_KeyValueFromArgs(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("test", map[string]string{})
	mgr := newTestEnvManager()
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), &envSetFlags{}, []string{"MY_KEY", "my_value"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "my_value", env.Getenv("MY_KEY"))
}

func Test_EnvSetAction_KeyEqualsValue(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("test", map[string]string{})
	mgr := newTestEnvManager()
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), &envSetFlags{}, []string{"MY_KEY=my_value"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "my_value", env.Getenv("MY_KEY"))
}

func Test_EnvSetAction_FromFile(t *testing.T) {
	t.Parallel()

	// Create a temp .env file
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	err := os.WriteFile(envFile, []byte("FILE_KEY=file_value\nFILE_KEY2=file_value2\n"), 0600)
	require.NoError(t, err)

	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("test", map[string]string{})
	mgr := newTestEnvManager()
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), &envSetFlags{file: envFile}, nil)
	_, err = action.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "file_value", env.Getenv("FILE_KEY"))
	assert.Equal(t, "file_value2", env.Getenv("FILE_KEY2"))
}

func Test_EnvSetAction_FileNotFound(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("test", map[string]string{})
	mgr := newTestEnvManager()

	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), &envSetFlags{file: "/nonexistent"}, nil)
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

func Test_EnvSetAction_CaseConflictWarning(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	// Env already has MY_KEY
	env := environment.NewWithValues("test", map[string]string{"MY_KEY": "old"})
	mgr := newTestEnvManager()
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	// Setting my_key (different case) - should trigger warning but still succeed
	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), &envSetFlags{}, []string{"my_key=new_value"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	// The value should still be set
	assert.Equal(t, "new_value", env.Getenv("my_key"))
}

// --- envListAction Tests ---

func Test_EnvListAction_JsonFormat(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return(
		[]*environment.Description{
			{Name: "env1", HasLocal: true, IsDefault: true},
			{Name: "env2", HasLocal: true},
		}, nil,
	)

	buf := &bytes.Buffer{}
	action := newEnvListAction(mgr, azdCtx, &output.JsonFormatter{}, buf)
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Contains(t, buf.String(), "env1")
	assert.Contains(t, buf.String(), "env2")
}

func Test_EnvListAction_NoneFormat(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return([]*environment.Description{}, nil)

	buf := &bytes.Buffer{}
	action := newEnvListAction(mgr, azdCtx, &output.NoneFormatter{}, buf)
	// NoneFormatter returns error when data is present, but this covers the code path
	_, _ = action.Run(context.Background())
	_ = azdCtx // used in constructor
}

func Test_EnvListAction_ListError(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return(([]*environment.Description)(nil), assert.AnError)

	buf := &bytes.Buffer{}
	action := newEnvListAction(mgr, azdCtx, &output.JsonFormatter{}, buf)
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

// --- envGetValuesAction Tests ---

func Test_EnvGetValuesAction_Success(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	azdCtx := newTestAzdContext(t)

	// Set default environment so Run can find it
	err := azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"})
	require.NoError(t, err)

	env := environment.NewWithValues("test", map[string]string{"KEY1": "val1", "KEY2": "val2"})
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "test").Return(env, nil)

	buf := &bytes.Buffer{}
	action := newEnvGetValuesAction(
		azdCtx, mgr, mockCtx.Console,
		&output.JsonFormatter{}, buf,
		&envGetValuesFlags{},
	)
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Contains(t, buf.String(), "KEY1")
}

// --- envGetValueAction Tests ---

func Test_EnvGetValueAction_NoArgs(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()

	action := newEnvGetValueAction(
		azdCtx, mgr, mockCtx.Console,
		&bytes.Buffer{}, &envGetValueFlags{}, nil,
	)
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

func Test_EnvGetValueAction_Success(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	azdCtx := newTestAzdContext(t)

	// Set default environment
	err := azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "test"})
	require.NoError(t, err)

	env := environment.NewWithValues("test", map[string]string{"MYKEY": "myval"})
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "test").Return(env, nil)

	buf := &bytes.Buffer{}
	action := newEnvGetValueAction(
		azdCtx, mgr, mockCtx.Console,
		buf, &envGetValueFlags{}, []string{"MYKEY"},
	)
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Contains(t, buf.String(), "myval")
}

// --- envRemoveAction Tests ---

func Test_EnvRemoveAction_Confirmed(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)

	// Set default env name so the action knows which env to remove
	err := azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "testenv"})
	require.NoError(t, err)

	mc := mockinput.NewMockConsole()
	mc.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(true)

	mgr := newTestEnvManager()
	envDesc := &environment.Description{Name: "testenv", HasLocal: true}
	mgr.On("List", mock.Anything).Return([]*environment.Description{envDesc}, nil)
	mgr.On("Delete", "testenv").Return(nil)

	buf := &bytes.Buffer{}
	action := newEnvRemoveAction(
		azdCtx, mgr, mc,
		&output.NoneFormatter{}, buf,
		&envRemoveFlags{}, nil,
	)
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_EnvRemoveAction_Force(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)

	// Set default env name
	err := azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "testenv"})
	require.NoError(t, err)

	mgr := newTestEnvManager()
	envDesc := &environment.Description{Name: "testenv", HasLocal: true}
	mgr.On("List", mock.Anything).Return([]*environment.Description{envDesc}, nil)
	mgr.On("Delete", "testenv").Return(nil)

	buf := &bytes.Buffer{}
	action := newEnvRemoveAction(
		azdCtx, mgr, mockinput.NewMockConsole(),
		&output.NoneFormatter{}, buf,
		&envRemoveFlags{force: true}, nil,
	)
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	require.NotNil(t, result)
}

func Test_EnvRemoveAction_EnvNotFound(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)

	// Set default env name to one that won't be in the list
	err := azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "nonexistent"})
	require.NoError(t, err)

	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return([]*environment.Description{}, nil)

	buf := &bytes.Buffer{}
	action := newEnvRemoveAction(
		azdCtx, mgr, mockinput.NewMockConsole(),
		&output.NoneFormatter{}, buf,
		&envRemoveFlags{force: true}, nil,
	)
	_, err = action.Run(context.Background())
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// envNewAction.Run tests
// ---------------------------------------------------------------------------

func Test_EnvNewAction_OnlyEnv(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("newenv", nil)
	mgr := newTestEnvManager()
	mgr.On("Create", mock.Anything, mock.Anything).Return(env, nil)
	// Only one env => auto-set as default
	mgr.On("List", mock.Anything).Return([]*environment.Description{
		{Name: "newenv"},
	}, nil)

	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, []string{"newenv"}, mockinput.NewMockConsole())
	_, err := action.Run(context.Background())
	require.NoError(t, err)

	// Verify it was set as default
	defaultName, err := azdCtx.GetDefaultEnvironmentName()
	require.NoError(t, err)
	require.Equal(t, "newenv", defaultName)
}

func Test_EnvNewAction_CreateError(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("Create", mock.Anything, mock.Anything).
		Return((*environment.Environment)(nil), fmt.Errorf("creation failed"))

	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, []string{"newenv"}, mockinput.NewMockConsole())
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating new environment")
}

func Test_EnvNewAction_MultipleEnvs_NoPromptMode(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("env2", nil)
	mc := mockinput.NewMockConsole()
	mc.SetNoPromptMode(true)

	mgr := newTestEnvManager()
	mgr.On("Create", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("List", mock.Anything).Return([]*environment.Description{
		{Name: "env1"},
		{Name: "env2"},
	}, nil)

	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, []string{"env2"}, mc)
	_, err := action.Run(context.Background())
	require.NoError(t, err)

	defaultName, err := azdCtx.GetDefaultEnvironmentName()
	require.NoError(t, err)
	require.Equal(t, "env2", defaultName)
}

func Test_EnvNewAction_MultipleEnvs_UserConfirms(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("env2", nil)
	mc := mockinput.NewMockConsole()
	mc.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(true)

	mgr := newTestEnvManager()
	mgr.On("Create", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("List", mock.Anything).Return([]*environment.Description{
		{Name: "env1"},
		{Name: "env2"},
	}, nil)

	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, []string{"env2"}, mc)
	_, err := action.Run(context.Background())
	require.NoError(t, err)

	defaultName, err := azdCtx.GetDefaultEnvironmentName()
	require.NoError(t, err)
	require.Equal(t, "env2", defaultName)
}

func Test_EnvNewAction_MultipleEnvs_UserDeclines(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	// Set existing default
	err := azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "env1"})
	require.NoError(t, err)

	env := environment.NewWithValues("env2", nil)
	mc := mockinput.NewMockConsole()
	mc.WhenConfirm(func(options input.ConsoleOptions) bool { return true }).Respond(false)

	mgr := newTestEnvManager()
	mgr.On("Create", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("List", mock.Anything).Return([]*environment.Description{
		{Name: "env1"},
		{Name: "env2"},
	}, nil)

	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, []string{"env2"}, mc)
	_, err = action.Run(context.Background())
	require.NoError(t, err)

	// Default should still be env1
	defaultName, err := azdCtx.GetDefaultEnvironmentName()
	require.NoError(t, err)
	require.Equal(t, "env1", defaultName)
}

// ---------------------------------------------------------------------------
// envSelectAction.Run tests
// ---------------------------------------------------------------------------

func Test_EnvSelectAction_WithArgs(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("target-env", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "target-env").Return(env, nil)

	action := newEnvSelectAction(azdCtx, mgr, mockinput.NewMockConsole(), []string{"target-env"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)

	defaultName, err := azdCtx.GetDefaultEnvironmentName()
	require.NoError(t, err)
	require.Equal(t, "target-env", defaultName)
}

func Test_EnvSelectAction_NotFound(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "no-such-env").
		Return((*environment.Environment)(nil), environment.ErrNotFound)

	action := newEnvSelectAction(azdCtx, mgr, mockinput.NewMockConsole(), []string{"no-such-env"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func Test_EnvSelectAction_EmptyList(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return([]*environment.Description{}, nil)

	action := newEnvSelectAction(azdCtx, mgr, mockinput.NewMockConsole(), nil)
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

func Test_EnvSelectAction_PromptSelection(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("env2", nil)

	mc := mockinput.NewMockConsole()
	mc.WhenSelect(func(options input.ConsoleOptions) bool { return true }).Respond(1) // select index 1 = "env2"

	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return([]*environment.Description{
		{Name: "env1"},
		{Name: "env2"},
	}, nil)
	mgr.On("Get", mock.Anything, "env2").Return(env, nil)

	action := newEnvSelectAction(azdCtx, mgr, mc, nil)
	_, err := action.Run(context.Background())
	require.NoError(t, err)

	defaultName, err := azdCtx.GetDefaultEnvironmentName()
	require.NoError(t, err)
	require.Equal(t, "env2", defaultName)
}

// ---------------------------------------------------------------------------
// envListAction.Run tests (table/json paths)
// ---------------------------------------------------------------------------

func Test_EnvListAction_TableFormat(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return([]*environment.Description{
		{Name: "env1", HasLocal: true, IsDefault: true},
		{Name: "env2", HasLocal: true},
	}, nil)

	buf := &bytes.Buffer{}
	action := newEnvListAction(mgr, azdCtx, &output.TableFormatter{}, buf)
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "env1")
}

func Test_EnvListAction_Empty(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return([]*environment.Description{}, nil)

	buf := &bytes.Buffer{}
	action := newEnvListAction(mgr, azdCtx, &output.JsonFormatter{}, buf)
	_, err := action.Run(context.Background())
	require.NoError(t, err)
}

// --- envNewAction constructor ---

func Test_EnvNewAction_Constructor(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, nil, mockinput.NewMockConsole())
	require.NotNil(t, action)
}

// --- envSelectAction constructor ---

func Test_EnvSelectAction_Constructor(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	action := newEnvSelectAction(azdCtx, mgr, mockinput.NewMockConsole(), nil)
	require.NotNil(t, action)
}

// --- Smoke test for envSetAction constructor ---

func Test_EnvSetAction_Constructor(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("test", map[string]string{})
	mgr := newTestEnvManager()
	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), &envSetFlags{}, nil)
	require.NotNil(t, action)
}

// --- envGetValuesAction constructor ---

func Test_EnvGetValuesAction_Constructor(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	action := newEnvGetValuesAction(
		azdCtx, mgr, mockCtx.Console, &output.JsonFormatter{},
		&bytes.Buffer{}, &envGetValuesFlags{},
	)
	require.NotNil(t, action)
}

// --- envGetValueAction constructor ---

func Test_EnvGetValueAction_Constructor(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	action := newEnvGetValueAction(azdCtx, mgr, mockCtx.Console, &bytes.Buffer{}, &envGetValueFlags{}, nil)
	require.NotNil(t, action)
}

// --- configUserConfigManager helper test ---

func Test_NewUserConfigManagerFromMock(t *testing.T) {
	t.Parallel()
	mockCtx := mocks.NewMockContext(context.Background())
	ucm := config.NewUserConfigManager(mockCtx.ConfigManager)
	require.NotNil(t, ucm)
	cfg, err := ucm.Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

// ---------------------------------------------------------------------------
// envConfigGetAction.Run tests
// ---------------------------------------------------------------------------

func Test_EnvConfigGetAction_Success(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	env.Config.Set("mykey", "myval")

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").Return(env, nil)

	buf := &bytes.Buffer{}
	action := newEnvConfigGetAction(
		azdCtx, mgr,
		&output.JsonFormatter{}, buf,
		&envConfigGetFlags{}, []string{"mykey"},
	)
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "myval")
}

func Test_EnvConfigGetAction_KeyNotFound(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").Return(env, nil)

	buf := &bytes.Buffer{}
	action := newEnvConfigGetAction(
		azdCtx, mgr,
		&output.JsonFormatter{}, buf,
		&envConfigGetFlags{}, []string{"no-such-key"},
	)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value at path")
}

func Test_EnvConfigGetAction_EnvNotFound(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").
		Return((*environment.Environment)(nil), environment.ErrNotFound)

	buf := &bytes.Buffer{}
	action := newEnvConfigGetAction(
		azdCtx, mgr,
		&output.JsonFormatter{}, buf,
		&envConfigGetFlags{}, []string{"somekey"},
	)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func Test_EnvConfigGetAction_WithFlagOverride(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "default"}))

	env := environment.NewWithValues("other", nil)
	env.Config.Set("a.b", "nested")
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "other").Return(env, nil)

	buf := &bytes.Buffer{}
	flags := &envConfigGetFlags{}
	flags.EnvironmentName = "other"
	action := newEnvConfigGetAction(azdCtx, mgr, &output.JsonFormatter{}, buf, flags, []string{"a.b"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "nested")
}

// ---------------------------------------------------------------------------
// envConfigSetAction.Run tests
// ---------------------------------------------------------------------------

func Test_EnvConfigSetAction_Success(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvConfigSetAction(azdCtx, mgr, &envConfigSetFlags{}, []string{"path.key", "value1"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)

	val, ok := env.Config.Get("path.key")
	require.True(t, ok)
	require.Equal(t, "value1", val)
}

func Test_EnvConfigSetAction_EnvNotFound(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").
		Return((*environment.Environment)(nil), environment.ErrNotFound)

	action := newEnvConfigSetAction(azdCtx, mgr, &envConfigSetFlags{}, []string{"k", "v"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func Test_EnvConfigSetAction_JsonValue(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvConfigSetAction(azdCtx, mgr, &envConfigSetFlags{}, []string{"num", "42"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)

	val, ok := env.Config.Get("num")
	require.True(t, ok)
	require.Equal(t, float64(42), val) // JSON numbers become float64
}

func Test_EnvConfigSetAction_WithFlagOverride(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "default"}))

	env := environment.NewWithValues("other", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "other").Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	flags := &envConfigSetFlags{}
	flags.EnvironmentName = "other"
	action := newEnvConfigSetAction(azdCtx, mgr, flags, []string{"k", "v"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// envConfigUnsetAction.Run tests
// ---------------------------------------------------------------------------

func Test_EnvConfigUnsetAction_Success(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	env.Config.Set("remove.me", "val")
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvConfigUnsetAction(azdCtx, mgr, &envConfigUnsetFlags{}, []string{"remove.me"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)

	_, ok := env.Config.Get("remove.me")
	require.False(t, ok)
}

func Test_EnvConfigUnsetAction_EnvNotFound(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").
		Return((*environment.Environment)(nil), environment.ErrNotFound)

	action := newEnvConfigUnsetAction(azdCtx, mgr, &envConfigUnsetFlags{}, []string{"k"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func Test_EnvConfigUnsetAction_WithFlagOverride(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "default"}))

	env := environment.NewWithValues("other", nil)
	env.Config.Set("x", "y")
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "other").Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	flags := &envConfigUnsetFlags{}
	flags.EnvironmentName = "other"
	action := newEnvConfigUnsetAction(azdCtx, mgr, flags, []string{"x"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// envGetValuesAction.Run tests
// ---------------------------------------------------------------------------

func Test_EnvGetValuesAction_SuccessJson(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", map[string]string{"KEY1": "val1", "KEY2": "val2"})
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").Return(env, nil)

	buf := &bytes.Buffer{}
	action := newEnvGetValuesAction(
		azdCtx, mgr, mockinput.NewMockConsole(),
		&output.JsonFormatter{}, buf,
		&envGetValuesFlags{},
	)
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "KEY1")
}

func Test_EnvGetValuesAction_WithFlagOverride(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "default"}))

	env := environment.NewWithValues("other", map[string]string{"A": "B"})
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "other").Return(env, nil)

	buf := &bytes.Buffer{}
	flags := &envGetValuesFlags{}
	flags.EnvironmentName = "other"
	action := newEnvGetValuesAction(azdCtx, mgr, mockinput.NewMockConsole(), &output.JsonFormatter{}, buf, flags)
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "A")
}

func Test_EnvGetValuesAction_NotFound(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").
		Return((*environment.Environment)(nil), environment.ErrNotFound)

	buf := &bytes.Buffer{}
	action := newEnvGetValuesAction(
		azdCtx, mgr, mockinput.NewMockConsole(),
		&output.JsonFormatter{}, buf,
		&envGetValuesFlags{},
	)
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// envGetValueAction.Run tests (additional branches)
// ---------------------------------------------------------------------------

func Test_EnvGetValueAction_WithFlagOverride(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "default"}))

	env := environment.NewWithValues("other", map[string]string{"MY_KEY": "val123"})
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "other").Return(env, nil)

	buf := &bytes.Buffer{}
	flags := &envGetValueFlags{}
	flags.EnvironmentName = "other"
	action := newEnvGetValueAction(azdCtx, mgr, mockinput.NewMockConsole(), buf, flags, []string{"MY_KEY"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "val123")
}

func Test_EnvGetValueAction_EnvNotFound(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").
		Return((*environment.Environment)(nil), environment.ErrNotFound)

	buf := &bytes.Buffer{}
	action := newEnvGetValueAction(
		azdCtx, mgr, mockinput.NewMockConsole(), buf,
		&envGetValueFlags{}, []string{"somekey"},
	)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not exist")
}

func Test_EnvGetValueAction_KeyNotFound(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", map[string]string{"OTHER": "val"})
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "myenv").Return(env, nil)

	buf := &bytes.Buffer{}
	action := newEnvGetValueAction(
		azdCtx, mgr, mockinput.NewMockConsole(), buf,
		&envGetValueFlags{}, []string{"NO_SUCH_KEY"},
	)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "NO_SUCH_KEY")
}

// ---------------------------------------------------------------------------
// envListAction.Run - List error branch with detail
// ---------------------------------------------------------------------------

func Test_EnvListAction_ListError_DetailedMessage(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return(([]*environment.Description)(nil), fmt.Errorf("list failed"))

	buf := &bytes.Buffer{}
	action := newEnvListAction(mgr, azdCtx, &output.JsonFormatter{}, buf)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing environments")
}

// ---------------------------------------------------------------------------
// envNewAction.Run - List error branch
// ---------------------------------------------------------------------------

func Test_EnvNewAction_ListError(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	env := environment.NewWithValues("env1", nil)
	mgr := newTestEnvManager()
	mgr.On("Create", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("List", mock.Anything).Return(([]*environment.Description)(nil), fmt.Errorf("list error"))

	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, []string{"env1"}, mockinput.NewMockConsole())
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing environments")
}

// ---------------------------------------------------------------------------
// envSelectAction.Run - list error, get error (not ErrNotFound)
// ---------------------------------------------------------------------------

func Test_EnvSelectAction_ListError(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("List", mock.Anything).Return(([]*environment.Description)(nil), fmt.Errorf("fail"))

	action := newEnvSelectAction(azdCtx, mgr, mockinput.NewMockConsole(), nil)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing environments")
}

func Test_EnvSelectAction_GetError(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, "env1").
		Return((*environment.Environment)(nil), fmt.Errorf("unexpected error"))

	action := newEnvSelectAction(azdCtx, mgr, mockinput.NewMockConsole(), []string{"env1"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "ensuring environment exists")
}

// ---------------------------------------------------------------------------
// parseConfigValue tests (covering more branches)
// ---------------------------------------------------------------------------

func Test_ParseConfigValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected any
	}{
		{"hello", "hello"},
		{"42", float64(42)},
		{"3.14", float64(3.14)},
		{"true", true},
		{"false", false},
		{`{"a":"b"}`, map[string]any{"a": "b"}},
		{`[1,2,3]`, []any{float64(1), float64(2), float64(3)}},
		{"null", "null"}, // null stays as string
		{`"true"`, "true"},
		{`"8080"`, "8080"},
		{"not json {", "not json {"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseConfigValue(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}
