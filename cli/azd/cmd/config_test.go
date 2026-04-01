// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
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
	mockContext := mocks.NewMockContext(context.Background())
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
	mockContext := mocks.NewMockContext(context.Background())
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
	mockContext := mocks.NewMockContext(context.Background())
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
	mockContext := mocks.NewMockContext(context.Background())

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
