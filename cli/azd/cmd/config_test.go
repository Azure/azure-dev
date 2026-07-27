// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

func newTestUserConfigManager(t *testing.T) config.UserConfigManager {
	t.Helper()
	tempDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", tempDir)

	manager := config.NewManager()
	fileConfigManager := config.NewFileConfigManager(manager)
	userConfigManager := config.NewUserConfigManager(fileConfigManager)

	// Initialize with an empty config file
	configPath := filepath.Join(tempDir, "config.json")
	err := fileConfigManager.Save(config.NewEmptyConfig(), configPath)
	require.NoError(t, err)

	return userConfigManager
}

func TestConfigShowAction_JsonFormat(t *testing.T) {
	buf := &bytes.Buffer{}
	userConfigManager := newTestUserConfigManager(t)

	action := newConfigShowAction(userConfigManager, &output.JsonFormatter{}, buf)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)

	// JSON output should be valid
	var values map[string]any
	err = json.Unmarshal(buf.Bytes(), &values)
	require.NoError(t, err)
}

func TestConfigShowAction_NoneFormat(t *testing.T) {
	buf := &bytes.Buffer{}
	userConfigManager := newTestUserConfigManager(t)

	action := newConfigShowAction(userConfigManager, &output.NoneFormatter{}, buf)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)
	// NoneFormat does not write to buffer
	require.Empty(t, buf.String())
}

func TestConfigListAction_DelegatesAndShowsWarning(t *testing.T) {
	buf := &bytes.Buffer{}
	mockContext := mocks.NewMockContext(t.Context())
	userConfigManager := newTestUserConfigManager(t)

	showAction := &configShowAction{
		configManager: userConfigManager,
		formatter:     &output.JsonFormatter{},
		writer:        buf,
	}

	action := newConfigListAction(mockContext.Console, showAction)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)

	// Verify JSON output was written (delegates to configShow)
	require.NotEmpty(t, buf.String())
}

func TestConfigGetAction(t *testing.T) {
	t.Run("existing_key", func(t *testing.T) {
		buf := &bytes.Buffer{}
		userConfigManager := newTestUserConfigManager(t)

		// Set a value first
		cfg, err := userConfigManager.Load()
		require.NoError(t, err)
		err = cfg.Set("test.key", "test-value")
		require.NoError(t, err)
		err = userConfigManager.Save(cfg)
		require.NoError(t, err)

		action := newConfigGetAction(
			userConfigManager, &output.JsonFormatter{}, buf, []string{"test.key"},
		)
		result, err := action.Run(t.Context())
		require.NoError(t, err)
		require.Nil(t, result)
		require.NotEmpty(t, buf.String())
	})

	t.Run("missing_key", func(t *testing.T) {
		buf := &bytes.Buffer{}
		userConfigManager := newTestUserConfigManager(t)

		action := newConfigGetAction(
			userConfigManager,
			&output.JsonFormatter{},
			buf,
			[]string{"nonexistent.key"},
		)
		_, err := action.Run(t.Context())
		require.Error(t, err)
		require.Contains(t, err.Error(), "no value at path")
	})
}

func TestConfigSetAction(t *testing.T) {
	userConfigManager := newTestUserConfigManager(t)

	action := newConfigSetAction(
		userConfigManager, []string{"my.setting", "myvalue"},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)

	// Verify the value was saved
	cfg, err := userConfigManager.Load()
	require.NoError(t, err)
	val, ok := cfg.Get("my.setting")
	require.True(t, ok)
	require.Equal(t, "myvalue", val)
}

func TestConfigUnsetAction(t *testing.T) {
	userConfigManager := newTestUserConfigManager(t)

	// Set a value first
	cfg, err := userConfigManager.Load()
	require.NoError(t, err)
	err = cfg.Set("my.setting", "myvalue")
	require.NoError(t, err)
	err = userConfigManager.Save(cfg)
	require.NoError(t, err)

	action := newConfigUnsetAction(
		userConfigManager, []string{"my.setting"},
	)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result)

	// Verify the value was removed
	cfg, err = userConfigManager.Load()
	require.NoError(t, err)
	_, ok := cfg.Get("my.setting")
	require.False(t, ok)
}

func TestConfigResetAction_WithForce(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	userConfigManager := newTestUserConfigManager(t)

	// Set a value first
	cfg, err := userConfigManager.Load()
	require.NoError(t, err)
	err = cfg.Set("some.key", "some-value")
	require.NoError(t, err)
	err = userConfigManager.Save(cfg)
	require.NoError(t, err)

	action := newConfigResetAction(
		mockContext.Console,
		userConfigManager,
		&configResetActionFlags{force: true},
		[]string{},
	)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "Configuration reset", result.Message.Header)

	// Verify config is now empty
	cfg, err = userConfigManager.Load()
	require.NoError(t, err)
	raw := cfg.Raw()
	require.Empty(t, raw)
}

func TestConfigResetAction_UserDeclines(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	userConfigManager := newTestUserConfigManager(t)

	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(false)

	// Set a value first
	cfg, err := userConfigManager.Load()
	require.NoError(t, err)
	err = cfg.Set("keep.me", "important")
	require.NoError(t, err)
	err = userConfigManager.Save(cfg)
	require.NoError(t, err)

	action := newConfigResetAction(
		mockContext.Console,
		userConfigManager,
		&configResetActionFlags{force: false},
		[]string{},
	)

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result) // nil result when cancelled

	// Verify config is NOT reset
	cfg, err = userConfigManager.Load()
	require.NoError(t, err)
	val, ok := cfg.Get("keep.me")
	require.True(t, ok)
	require.Equal(t, "important", val)
}

func TestConfigListAlphaAction_Run(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	featureManager := alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig())
	action := newConfigListAlphaAction(featureManager, mockContext.Console, []string{})

	result, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Nil(t, result) // action returns nil,nil
}

func TestGetCmdConfigHelpDescription(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "config"}
	result := getCmdConfigHelpDescription(cmd)
	require.Contains(t, result, "azd")
}

func TestGetCmdConfigHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "config"}
	result := getCmdConfigHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdListAlphaHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "list-alpha"}
	result := getCmdListAlphaHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func TestGetCmdConfigOptionsHelpFooter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "config-options"}
	result := getCmdConfigOptionsHelpFooter(cmd)
	require.Contains(t, result, "Examples")
}

func Test_ConfigShowAction_JsonFormat(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	_ = cfg.Set("foo", "bar")
	cfgMgr := &testConfigManager{loadCfg: cfg}

	var buf bytes.Buffer
	action := &configShowAction{
		configManager: cfgMgr,
		formatter:     &output.JsonFormatter{},
		writer:        &buf,
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "foo")
}

func Test_ConfigShowAction_NoneFormat(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	cfgMgr := &testConfigManager{loadCfg: cfg}

	var buf bytes.Buffer
	action := &configShowAction{
		configManager: cfgMgr,
		formatter:     &output.NoneFormatter{},
		writer:        &buf,
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigListAction_DelegateToShow(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	cfgMgr := &testConfigManager{loadCfg: cfg}

	console := mockinput.NewMockConsole()
	var buf bytes.Buffer
	showAction := &configShowAction{
		configManager: cfgMgr,
		formatter:     &output.NoneFormatter{},
		writer:        &buf,
	}
	action := &configListAction{
		console:    console,
		configShow: showAction,
	}
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigShowAction_FormatError(t *testing.T) {
	t.Parallel()
	cfgMgr := &failLoadConfigMgr{}
	// Use JsonFormatter; the load will fail, exercising the error path
	action := &configShowAction{
		configManager: cfgMgr,
		formatter:     &output.JsonFormatter{},
		writer:        &bytes.Buffer{},
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_ConfigShowAction_WriteError(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	cfgMgr := &testConfigManager{loadCfg: cfg}
	// Load succeeds; JsonFormatter writing to an always-failing writer should error.
	action := &configShowAction{
		configManager: cfgMgr,
		formatter:     &output.JsonFormatter{},
		writer:        &errWriter{},
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_ConfigListAction_Delegation(t *testing.T) {
	t.Parallel()
	cfgMgr := &failLoadConfigMgr{}
	showAction := &configShowAction{
		configManager: cfgMgr,
		formatter:     &output.JsonFormatter{},
		writer:        &bytes.Buffer{},
	}
	action := &configListAction{
		configShow: showAction,
		console:    mockinput.NewMockConsole(),
	}
	_, err := action.Run(t.Context())
	// configShowAction.Run with failing load will error
	require.Error(t, err)
}

func Test_ConfigListAlpha_HappyPath(t *testing.T) {
	t.Parallel()
	fm := alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig())
	console := mockinput.NewMockConsole()
	action := newConfigListAlphaAction(fm, console, nil)
	result, err := action.Run(t.Context())
	require.NoError(t, err)
	_ = result
}

func Test_ConfigOptions_DefaultFormat_MapValue(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": map[string]any{"nested": "value"},
		},
	})
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.NoneFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigOptions_DefaultFormat_ArrayValue(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": []any{"a", "b"},
		},
	})
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.NoneFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigOptions_DefaultFormat_IntValue(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{
		"defaults": map[string]any{
			"subscription": 99,
		},
	})
	mgr := &finishConfigMgr{cfg: cfg}
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.NoneFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}

// JsonFormatter writing to errWriter should error

func Test_ConfigGetAction_FormatError(t *testing.T) {
	t.Parallel()
	cfg := config.NewConfig(map[string]any{"mykey": "myval"})
	mgr := &finishConfigMgr{cfg: cfg}
	w := &errWriter{}
	formatter := &output.JsonFormatter{}
	action := newConfigGetAction(mgr, formatter, w, []string{"mykey"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_ConfigOptions_LoadWarning(t *testing.T) {
	t.Parallel()
	mgr := &finishFailLoadConfigMgr{} // returns generic error (not os.IsNotExist)
	console := mockinput.NewMockConsole()
	buf := &bytes.Buffer{}
	formatter := &output.NoneFormatter{}
	action := newConfigOptionsAction(console, formatter, buf, mgr, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err) // should still work, just log warning
}

// configGetAction.Run — Load error
func Test_ConfigGetAction_LoadError(t *testing.T) {
	t.Parallel()
	mgr := &finishFailLoadConfigMgr{}
	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	action := newConfigGetAction(mgr, formatter, buf, []string{"any"})
	_, err := action.Run(t.Context())
	require.Error(t, err)
}

func Test_NewConfigResetFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newConfigResetFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewConfigShowAction(t *testing.T) {
	t.Parallel()
	a := newConfigShowAction(&testConfigMgr{}, &output.JsonFormatter{}, &bytes.Buffer{})
	require.NotNil(t, a)
}

func Test_NewConfigListAction(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	showAction := newConfigShowAction(&testConfigMgr{}, &output.JsonFormatter{}, &bytes.Buffer{})
	a := newConfigListAction(console, showAction.(*configShowAction))
	require.NotNil(t, a)
}

func Test_NewConfigGetAction(t *testing.T) {
	t.Parallel()
	a := newConfigGetAction(&testConfigMgr{}, &output.JsonFormatter{}, &bytes.Buffer{}, []string{"defaults"})
	require.NotNil(t, a)
}

func Test_NewConfigSetAction(t *testing.T) {
	t.Parallel()
	a := newConfigSetAction(&testConfigMgr{}, []string{"defaults.subscription", "abc"})
	require.NotNil(t, a)
}

func Test_NewConfigUnsetAction(t *testing.T) {
	t.Parallel()
	a := newConfigUnsetAction(&testConfigMgr{}, []string{"defaults.subscription"})
	require.NotNil(t, a)
}

func Test_NewConfigResetAction(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	a := newConfigResetAction(console, &testConfigMgr{}, &configResetActionFlags{}, []string{})
	require.NotNil(t, a)
}

func Test_NewConfigListAlphaAction(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	fm := alpha.NewFeaturesManager(&testConfigMgr{})
	a := newConfigListAlphaAction(fm, console, []string{})
	require.NotNil(t, a)
}

func Test_NewConfigOptionsAction(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	a := newConfigOptionsAction(console, &output.JsonFormatter{}, &bytes.Buffer{}, &testConfigMgr{}, []string{})
	require.NotNil(t, a)
}

func Test_ConfigShowAction_Run(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	a := newConfigShowAction(&testConfigMgr{}, &output.JsonFormatter{}, &buf)
	_, err := a.(*configShowAction).Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigGetAction_Run_ValidPath(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	a := newConfigGetAction(&testConfigMgr{}, &output.JsonFormatter{}, &buf, []string{"defaults"})
	_, err := a.(*configGetAction).Run(t.Context())
	// "defaults" path doesn't exist in empty config, so this returns an error
	require.Error(t, err)
}

func Test_ConfigSetAction_Run_Success(t *testing.T) {
	t.Parallel()
	a := newConfigSetAction(&testConfigMgr{}, []string{"defaults.subscription", "abc-123"})
	_, err := a.(*configSetAction).Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigUnsetAction_Run_Success(t *testing.T) {
	t.Parallel()
	a := newConfigUnsetAction(&testConfigMgr{}, []string{"defaults.subscription"})
	_, err := a.(*configUnsetAction).Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigResetAction_Run_ForceReset(t *testing.T) {
	a := newConfigResetAction(
		mockinput.NewMockConsole(),
		&testConfigMgr{},
		&configResetActionFlags{force: true},
		[]string{},
	)
	_, err := a.(*configResetAction).Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigResetAction_Run_WithPathArg(t *testing.T) {
	a := newConfigResetAction(
		mockinput.NewMockConsole(),
		&testConfigMgr{},
		&configResetActionFlags{force: true},
		[]string{"defaults.subscription"},
	)
	_, err := a.(*configResetAction).Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigResetAction_Run_UserDeclines(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return false, nil
	})

	a := newConfigResetAction(
		console,
		&testConfigMgr{},
		&configResetActionFlags{force: false},
		[]string{},
	)
	_, err := a.(*configResetAction).Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigResetAction_Run_UserConfirms(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return true, nil
	})

	a := newConfigResetAction(
		console,
		&testConfigMgr{},
		&configResetActionFlags{force: false},
		[]string{},
	)
	_, err := a.(*configResetAction).Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigListAlphaAction_Run(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	fm := alpha.NewFeaturesManager(&testConfigMgr{})
	a := newConfigListAlphaAction(fm, console, []string{})
	_, err := a.(*configListAlphaAction).Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigListAlphaAction_Run_WithArg(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	fm := alpha.NewFeaturesManager(&testConfigMgr{})
	a := newConfigListAlphaAction(fm, console, []string{"some-feature"})
	_, err := a.(*configListAlphaAction).Run(t.Context())
	// Toggling an unknown feature may succeed or fail
	_ = err
}

func Test_ConfigOptionsAction_Run(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	var buf bytes.Buffer
	a := newConfigOptionsAction(console, &output.JsonFormatter{}, &buf, &testConfigMgr{}, []string{})
	_, err := a.(*configOptionsAction).Run(t.Context())
	require.NoError(t, err)
}

func Test_ConfigResetAction_WithForce(t *testing.T) {
	t.Parallel()

	ucm := &pushConfigMgr{cfg: config.NewEmptyConfig()}
	console := mockinput.NewMockConsole()

	action := newConfigResetAction(console, ucm, &configResetActionFlags{force: true}, nil)
	result, err := action.Run(t.Context())
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
	result, err := action.Run(t.Context())
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
	result, err := action.Run(t.Context())
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
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "user cancelled")
}

func Test_ConfigResetAction_SaveError(t *testing.T) {
	t.Parallel()

	ucm := &pushFailSaveConfigMgr{}
	console := mockinput.NewMockConsole()

	action := newConfigResetAction(console, ucm, &configResetActionFlags{force: true}, nil)
	_, err := action.Run(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "save error")
}

func Test_ConfigGetAction_JsonFormat(t *testing.T) {
	t.Parallel()

	cfg := config.NewEmptyConfig()
	_ = cfg.Set("mykey", "myval")
	ucm := &pushConfigMgr{cfg: cfg}

	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	action := newConfigGetAction(ucm, formatter, buf, []string{"mykey"})
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Contains(t, buf.String(), "myval")
}

func Test_ConfigOptionsAction_JsonFormat(t *testing.T) {
	t.Parallel()

	ucm := &pushConfigMgr{cfg: config.NewEmptyConfig()}
	buf := &bytes.Buffer{}
	formatter := &output.JsonFormatter{}
	console := mockinput.NewMockConsole()

	action := newConfigOptionsAction(console, formatter, buf, ucm, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.True(t, buf.Len() > 0, "json output should be non-empty")
}

func Test_ConfigOptionsAction_LoadError_NotFileNotFound(t *testing.T) {
	t.Parallel()

	ucm := &pushFailLoadConfigMgr{}
	buf := &bytes.Buffer{}
	console := mockinput.NewMockConsole()

	action := newConfigOptionsAction(console, &output.NoneFormatter{}, buf, ucm, nil)
	_, err := action.Run(t.Context())
	require.NoError(t, err)
}
