// Copyright (c) Microsoft Corporation. Licensed under the MIT License.
// Coverage push to reach 55% - targeting specific uncovered branches
package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ===========================================================================
// envSetAction.Run - file flag paths (currently uncovered)
// ===========================================================================

func Test_EnvSetAction_FileAndArgsMutuallyExclusive(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()

	flags := &envSetFlags{file: "some.env"}
	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), flags, []string{"KEY=VALUE"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot combine --file flag")
}

func Test_EnvSetAction_FileNotFound_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()

	flags := &envSetFlags{file: filepath.Join(t.TempDir(), "nonexistent.env")}
	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), flags, nil)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to open file")
}

func Test_EnvSetAction_FileSuccess(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	tmpDir := t.TempDir()
	envFile := filepath.Join(tmpDir, "test.env")
	require.NoError(t, os.WriteFile(envFile, []byte("FOO=bar\nBAZ=qux\n"), 0600))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	flags := &envSetFlags{file: envFile}
	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), flags, nil)
	_, err := action.Run(context.Background())
	require.NoError(t, err)
}

func Test_EnvSetAction_NoArgs_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()

	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), &envSetFlags{}, nil)
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

func Test_EnvSetAction_SingleKeyValuePair(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()
	mgr.On("Save", mock.Anything, mock.Anything).Return(nil)

	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), &envSetFlags{}, []string{"MYKEY", "MYVAL"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)
}

func Test_EnvSetAction_BadKeyValueFormat(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()

	action := newEnvSetAction(azdCtx, env, mgr, mockinput.NewMockConsole(), &envSetFlags{}, []string{"NOEQUALS"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

// ===========================================================================
// configResetAction.Run - force flag and confirm paths
// ===========================================================================

func Test_ConfigResetAction_WithForce_Push(t *testing.T) {
	t.Parallel()

	ucm := &pushConfigMgr{cfg: config.NewEmptyConfig()}
	console := mockinput.NewMockConsole()

	action := newConfigResetAction(console, ucm, &configResetActionFlags{force: true}, nil)
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "Configuration reset", result.Message.Header)
}

func Test_ConfigResetAction_ConfirmNo(t *testing.T) {
	t.Parallel()

	ucm := &pushConfigMgr{cfg: config.NewEmptyConfig()}
	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(false)

	action := newConfigResetAction(console, ucm, &configResetActionFlags{force: false}, nil)
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Nil(t, result)
}

func Test_ConfigResetAction_ConfirmYes(t *testing.T) {
	t.Parallel()

	ucm := &pushConfigMgr{cfg: config.NewEmptyConfig()}
	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(true)

	action := newConfigResetAction(console, ucm, &configResetActionFlags{force: false}, nil)
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "Configuration reset", result.Message.Header)
}

func Test_ConfigResetAction_ConfirmError(t *testing.T) {
	t.Parallel()

	ucm := &pushConfigMgr{cfg: config.NewEmptyConfig()}
	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return false, errors.New("tty error")
	})

	action := newConfigResetAction(console, ucm, &configResetActionFlags{force: false}, nil)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "user cancelled")
}

func Test_ConfigResetAction_SaveError_Push(t *testing.T) {
	t.Parallel()

	ucm := &pushFailSaveConfigMgr{}
	console := mockinput.NewMockConsole()

	action := newConfigResetAction(console, ucm, &configResetActionFlags{force: true}, nil)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "save error")
}

// ===========================================================================
// configSetAction.Run - save error and load error
// ===========================================================================

func Test_ConfigSetAction_SaveError_Push(t *testing.T) {
	t.Parallel()

	ucm := &pushFailSaveConfigMgr{}
	action := newConfigSetAction(ucm, []string{"key", "value"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "save error")
}

func Test_ConfigSetAction_LoadError_Push(t *testing.T) {
	t.Parallel()

	ucm := &pushFailLoadConfigMgr{}
	action := newConfigSetAction(ucm, []string{"key", "value"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "load error")
}

// ===========================================================================
// configUnsetAction.Run - save error and load error
// ===========================================================================

func Test_ConfigUnsetAction_SaveError_Push(t *testing.T) {
	t.Parallel()

	cfg := config.NewEmptyConfig()
	_ = cfg.Set("mykey", "val")
	ucm := &pushConfigMgr{cfg: cfg, saveErr: errors.New("save error")}

	action := newConfigUnsetAction(ucm, []string{"mykey"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "save error")
}

func Test_ConfigUnsetAction_LoadError_Push(t *testing.T) {
	t.Parallel()

	ucm := &pushFailLoadConfigMgr{}
	action := newConfigUnsetAction(ucm, []string{"mykey"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "load error")
}

// ===========================================================================
// configGetAction.Run - json format path + not-found path
// ===========================================================================

func Test_ConfigGetAction_JsonFormat_Push(t *testing.T) {
	t.Parallel()

	cfg := config.NewEmptyConfig()
	_ = cfg.Set("mykey", "myval")
	ucm := &pushConfigMgr{cfg: cfg}

	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	action := newConfigGetAction(ucm, formatter, buf, []string{"mykey"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "myval")
}

func Test_ConfigGetAction_NotFound_Push(t *testing.T) {
	t.Parallel()

	cfg := config.NewEmptyConfig()
	ucm := &pushConfigMgr{cfg: cfg}

	action := newConfigGetAction(ucm, &output.NoneFormatter{}, &bytes.Buffer{}, []string{"missing"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value at path")
}

// ===========================================================================
// configOptionsAction.Run - json format path
// ===========================================================================

func Test_ConfigOptionsAction_JsonFormat_Push(t *testing.T) {
	t.Parallel()

	ucm := &pushConfigMgr{cfg: config.NewEmptyConfig()}
	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	console := mockinput.NewMockConsole()

	action := newConfigOptionsAction(console, formatter, buf, ucm, nil)
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	require.True(t, buf.Len() > 0, "json output should be non-empty")
}

func Test_ConfigOptionsAction_LoadError_NotFileNotFound(t *testing.T) {
	t.Parallel()

	ucm := &pushFailLoadConfigMgr{}
	buf := &bytes.Buffer{}
	console := mockinput.NewMockConsole()

	action := newConfigOptionsAction(console, &output.NoneFormatter{}, buf, ucm, nil)
	_, err := action.Run(context.Background())
	require.NoError(t, err)
}

// ===========================================================================
// configShowAction.Run - json format success
// ===========================================================================

func Test_ConfigShowAction_JsonFormat_Push(t *testing.T) {
	t.Parallel()

	cfg := config.NewEmptyConfig()
	_ = cfg.Set("defaults.location", "eastus")
	ucm := &pushConfigMgr{cfg: cfg}

	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	action := newConfigShowAction(ucm, formatter, buf)
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "eastus")
}

// ===========================================================================
// uploadAction.Run (telemetry)
// ===========================================================================

func Test_UploadAction_NilTelemetrySystem(t *testing.T) {
	t.Parallel()
	action := newUploadAction(&internal.GlobalCommandOptions{})
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Nil(t, result)
}

// ===========================================================================
// envNewAction.Run - multiple envs path + create error
// ===========================================================================

func Test_EnvNewAction_MultipleEnvs_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	// Need a default env set for the "no" path to read
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "first"}))

	env := environment.NewWithValues("second", nil)
	mgr := newTestEnvManager()
	mgr.On("Create", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("List", mock.Anything).Return(
		[]*environment.Description{{Name: "first"}, {Name: "second"}}, nil,
	)

	console := mockinput.NewMockConsole()
	// With 2+ envs, it asks "Set new environment as default?" — answer no
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(false)

	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, []string{"second"}, console)
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	_ = result // envNewAction.Run returns (nil, nil) on success
}

func Test_EnvNewAction_MultipleEnvs_SetDefault_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)

	env := environment.NewWithValues("second", nil)
	mgr := newTestEnvManager()
	mgr.On("Create", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("List", mock.Anything).Return(
		[]*environment.Description{{Name: "first"}, {Name: "second"}}, nil,
	)

	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(true) // answer yes -> set as default

	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, []string{"second"}, console)
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	_ = result // envNewAction.Run returns (nil, nil) on success
}

func Test_EnvNewAction_ListError_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)

	env := environment.NewWithValues("newenv", nil)
	mgr := newTestEnvManager()
	mgr.On("Create", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("List", mock.Anything).Return(
		([]*environment.Description)(nil), errors.New("list failed"),
	)

	console := mockinput.NewMockConsole()
	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, []string{"newenv"}, console)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "listing environments")
}

func Test_EnvNewAction_CreateError_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)

	mgr := newTestEnvManager()
	mgr.On("Create", mock.Anything, mock.Anything).Return(
		(*environment.Environment)(nil), errors.New("create failed"),
	)

	console := mockinput.NewMockConsole()
	action := newEnvNewAction(azdCtx, mgr, &envNewFlags{}, []string{"newenv"}, console)
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "creating new environment")
}

// ===========================================================================
// envSelectAction.Run - success path exercising Get
// ===========================================================================

func Test_EnvSelectAction_Success_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(environment.NewWithValues("myenv", nil), nil)

	action := newEnvSelectAction(azdCtx, mgr, mockinput.NewMockConsole(), []string{"myenv"})
	result, err := action.Run(context.Background())
	require.NoError(t, err)
	_ = result
}

// ===========================================================================
// envGetValuesAction.Run - env load error
// ===========================================================================

func Test_EnvGetValuesAction_LoadError_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(
		(*environment.Environment)(nil), errors.New("env not found"),
	)

	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	flags := &envGetValuesFlags{EnvFlag: internal.EnvFlag{EnvironmentName: "myenv"}}

	action := newEnvGetValuesAction(azdCtx, mgr, console, formatter, buf, flags)
	_, err := action.Run(context.Background())
	require.Error(t, err)
}

// ===========================================================================
// envConfigSetAction.Run - save error
// ===========================================================================

func Test_EnvConfigSetAction_SaveError_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(errors.New("save error"))

	flags := &envConfigSetFlags{
		EnvFlag: internal.EnvFlag{EnvironmentName: "myenv"},
	}
	action := newEnvConfigSetAction(azdCtx, mgr, flags, []string{"key", "value"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "save error")
}

// ===========================================================================
// envConfigUnsetAction.Run - save error
// ===========================================================================

func Test_EnvConfigUnsetAction_SaveError_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	env.Config.Set("mykey", "val")
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)
	mgr.On("Save", mock.Anything, mock.Anything).Return(errors.New("save error"))

	flags := &envConfigUnsetFlags{
		EnvFlag: internal.EnvFlag{EnvironmentName: "myenv"},
	}
	action := newEnvConfigUnsetAction(azdCtx, mgr, flags, []string{"mykey"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "save error")
}

// ===========================================================================
// envConfigGetAction.Run - json format path and not found
// ===========================================================================

func Test_EnvConfigGetAction_JsonFormat_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	env.Config.Set("mykey", "myval")
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	flags := &envConfigGetFlags{
		EnvFlag: internal.EnvFlag{EnvironmentName: "myenv"},
	}
	action := newEnvConfigGetAction(azdCtx, mgr, formatter, buf, flags, []string{"mykey"})
	_, err := action.Run(context.Background())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "myval")
}

func Test_EnvConfigGetAction_NotFound_Push(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: "myenv"}))

	env := environment.NewWithValues("myenv", nil)
	mgr := newTestEnvManager()
	mgr.On("Get", mock.Anything, mock.Anything).Return(env, nil)

	flags := &envConfigGetFlags{
		EnvFlag: internal.EnvFlag{EnvironmentName: "myenv"},
	}
	action := newEnvConfigGetAction(azdCtx, mgr, &output.NoneFormatter{}, &bytes.Buffer{}, flags, []string{"missing"})
	_, err := action.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no value at path")
}

// ===========================================================================
// Mock types for this file
// ===========================================================================

type pushConfigMgr struct {
	cfg     config.Config
	saveErr error
}

func (m *pushConfigMgr) Load() (config.Config, error) {
	return m.cfg, nil
}

func (m *pushConfigMgr) Save(cfg config.Config) error {
	return m.saveErr
}

type pushFailSaveConfigMgr struct{}

func (m *pushFailSaveConfigMgr) Load() (config.Config, error) {
	return config.NewEmptyConfig(), nil
}

func (m *pushFailSaveConfigMgr) Save(cfg config.Config) error {
	return errors.New("save error")
}

type pushFailLoadConfigMgr struct{}

func (m *pushFailLoadConfigMgr) Load() (config.Config, error) {
	return nil, errors.New("load error")
}

func (m *pushFailLoadConfigMgr) Save(cfg config.Config) error {
	return nil
}
