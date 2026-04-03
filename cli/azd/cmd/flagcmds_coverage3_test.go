// Copyright (c) Microsoft Corporation. Licensed under the MIT License.
// Coverage3 – flag constructors, cmd constructors, action constructors, and Run() early paths.
// Each newXxxFlags call exercises flag-binding code; each newXxxCmd exercises command setup.
package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================
// newXxxFlags constructors – each exercises flag binding statements
// ================================================================

func Test_NewAuthLoginFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newAuthLoginFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewAuthStatusFlags_FC(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newAuthStatusFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewAuthTokenFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newAuthTokenFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewBuildFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newBuildFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewDownFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newDownFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewRestoreFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newRestoreFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewPackageFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newPackageFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewMonitorFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newMonitorFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewUpFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newUpFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewPipelineConfigFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newPipelineConfigFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewUpdateFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newUpdateFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewInitFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newInitFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewHooksRunFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newHooksRunFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewInfraCreateFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newInfraCreateFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewInfraDeleteFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newInfraDeleteFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewInfraGenerateFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newInfraGenerateFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewMcpStartFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newMcpStartFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewVersionFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newVersionFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewVsServerFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newVsServerFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewEnvSetFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newEnvSetFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewEnvSetSecretFlags_FC(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newEnvSetSecretFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewEnvNewFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newEnvNewFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewEnvRefreshFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newEnvRefreshFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewEnvGetValuesFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newEnvGetValuesFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewEnvGetValueFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newEnvGetValueFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewEnvConfigGetFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newEnvConfigGetFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewEnvConfigSetFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newEnvConfigSetFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewEnvConfigUnsetFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newEnvConfigUnsetFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewEnvRemoveFlags_FC(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newEnvRemoveFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewConfigResetFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newConfigResetFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewCopilotConsentListFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newCopilotConsentListFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewCopilotConsentGrantFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newCopilotConsentGrantFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewCopilotConsentRevokeFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newCopilotConsentRevokeFlags(cmd, global)
	require.NotNil(t, flags)
}

func Test_NewCompletionFigFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newCompletionFigFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewTemplateListFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newTemplateListFlags(cmd)
	require.NotNil(t, flags)
}

func Test_NewTemplateSourceAddFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	flags := newTemplateSourceAddFlags(cmd)
	require.NotNil(t, flags)
}

// ================================================================
// newXxxCmd constructors – each exercises command creation code
// ================================================================

func Test_NewAuthStatusCmd_FC(t *testing.T) {
	t.Parallel()
	cmd := newAuthStatusCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "status")
}

func Test_NewAuthTokenCmd(t *testing.T) {
	t.Parallel()
	cmd := newAuthTokenCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "token")
}

func Test_NewEnvSetCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvSetCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "set")
}

func Test_NewEnvSelectCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvSelectCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "select")
}

func Test_NewEnvListCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvListCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "list")
}

func Test_NewEnvNewCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvNewCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "new")
}

func Test_NewEnvRefreshCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvRefreshCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "refresh")
}

func Test_NewEnvGetValuesCmd_FC(t *testing.T) {
	t.Parallel()
	cmd := newEnvGetValuesCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "get-values")
}

func Test_NewEnvGetValueCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvGetValueCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "get-value")
}

func Test_NewEnvConfigGetCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvConfigGetCmd()
	require.NotNil(t, cmd)
}

func Test_NewEnvConfigSetCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvConfigSetCmd()
	require.NotNil(t, cmd)
}

func Test_NewEnvConfigUnsetCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvConfigUnsetCmd()
	require.NotNil(t, cmd)
}

func Test_NewEnvRemoveCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvRemoveCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "remove")
}

func Test_NewHooksRunCmd(t *testing.T) {
	t.Parallel()
	cmd := newHooksRunCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "run")
}

func Test_NewInfraGenerateCmd(t *testing.T) {
	t.Parallel()
	cmd := newInfraGenerateCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "generate")
}

func Test_NewInitCmd(t *testing.T) {
	t.Parallel()
	cmd := newInitCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "init")
}

func Test_NewTemplateListCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateListCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "list")
}

func Test_NewTemplateShowCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateShowCmd()
	require.NotNil(t, cmd)
	assert.Contains(t, cmd.Use, "show")
}

func Test_NewTemplateSourceListCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceListCmd()
	require.NotNil(t, cmd)
}

func Test_NewTemplateSourceAddCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceAddCmd()
	require.NotNil(t, cmd)
}

func Test_NewTemplateSourceRemoveCmd(t *testing.T) {
	t.Parallel()
	cmd := newTemplateSourceRemoveCmd()
	require.NotNil(t, cmd)
}

func Test_NewVsServerCmd(t *testing.T) {
	t.Parallel()
	cmd := newVsServerCmd()
	require.NotNil(t, cmd)
}

// ================================================================
// stringPtr and boolPtr coverage (auth_login.go value types)
// ================================================================

func Test_StringPtr_SetAndString(t *testing.T) {
	t.Parallel()
	var sp stringPtr

	// Before set, String() returns ""
	assert.Equal(t, "", sp.String())
	assert.Equal(t, "string", sp.Type())

	// After set
	err := sp.Set("hello")
	require.NoError(t, err)
	assert.Equal(t, "hello", sp.String())

	// Set empty string
	err = sp.Set("")
	require.NoError(t, err)
	assert.Equal(t, "", sp.String())
}

func Test_BoolPtr_SetAndString(t *testing.T) {
	t.Parallel()
	var bp boolPtr

	// Before set returns "false"
	assert.Equal(t, "false", bp.String())
	assert.Equal(t, "", bp.Type())

	// After set
	err := bp.Set("true")
	require.NoError(t, err)
	assert.Equal(t, "true", bp.String())
}

// ================================================================
// Action constructor coverage – simple constructors
// ================================================================

// testConfigMgr implements config.UserConfigManager for constructor tests
type testConfigMgr struct{}

func (m *testConfigMgr) Load() (config.Config, error) {
	return config.NewEmptyConfig(), nil
}
func (m *testConfigMgr) Save(c config.Config) error {
	return nil
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

func Test_NewVersionAction(t *testing.T) {
	t.Parallel()
	fm := alpha.NewFeaturesManager(&testConfigMgr{})
	console := mockinput.NewMockConsole()
	a := newVersionAction(&versionFlags{}, &output.JsonFormatter{}, &bytes.Buffer{}, console, fm)
	require.NotNil(t, a)
}

func Test_NewCompletionBashAction(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "root"}
	a := newCompletionBashAction(cmd)
	require.NotNil(t, a)
}

func Test_NewCompletionZshAction(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "root"}
	a := newCompletionZshAction(cmd)
	require.NotNil(t, a)
}

func Test_NewCompletionFishAction(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "root"}
	a := newCompletionFishAction(cmd)
	require.NotNil(t, a)
}

func Test_NewCompletionPowerShellAction(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "root"}
	a := newCompletionPowerShellAction(cmd)
	require.NotNil(t, a)
}

// ================================================================
// updateAction.Run – hits IsNonProdVersion early exit
// ================================================================

func Test_UpdateAction_Run_NonProdVersion(t *testing.T) {
	// In test builds, IsNonProdVersion() returns true, so Run exits immediately.
	console := mockinput.NewMockConsole()
	fm := alpha.NewFeaturesManager(&testConfigMgr{})
	a := newUpdateAction(
		&updateFlags{},
		console,
		&output.JsonFormatter{},
		&bytes.Buffer{},
		&testConfigMgr{},
		nil, // commandRunner not needed – early exit
		fm,
	)

	_, err := a.(*updateAction).Run(context.Background())
	require.Error(t, err)
	assert.True(t, errors.Is(err, internal.ErrUnsupportedOperation))
}

// ================================================================
// configShowAction.Run – exercises the config show path
// ================================================================

func Test_ConfigShowAction_Run(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	a := newConfigShowAction(&testConfigMgr{}, &output.JsonFormatter{}, &buf)
	_, err := a.(*configShowAction).Run(context.Background())
	require.NoError(t, err)
}

// ================================================================
// configGetAction.Run – exercises get path
// ================================================================

func Test_ConfigGetAction_Run_ValidPath(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	a := newConfigGetAction(&testConfigMgr{}, &output.JsonFormatter{}, &buf, []string{"defaults"})
	_, err := a.(*configGetAction).Run(context.Background())
	// "defaults" path doesn't exist in empty config, so this returns an error
	require.Error(t, err)
}

// ================================================================
// configSetAction.Run – exercises set path
// ================================================================

func Test_ConfigSetAction_Run_Success(t *testing.T) {
	t.Parallel()
	a := newConfigSetAction(&testConfigMgr{}, []string{"defaults.subscription", "abc-123"})
	_, err := a.(*configSetAction).Run(context.Background())
	require.NoError(t, err)
}

// ================================================================
// configUnsetAction.Run – exercises unset path
// ================================================================

func Test_ConfigUnsetAction_Run_Success(t *testing.T) {
	t.Parallel()
	a := newConfigUnsetAction(&testConfigMgr{}, []string{"defaults.subscription"})
	_, err := a.(*configUnsetAction).Run(context.Background())
	require.NoError(t, err)
}

// ================================================================
// configResetAction.Run – exercises reset path
// ================================================================

func Test_ConfigResetAction_Run_ForceReset(t *testing.T) {
	a := newConfigResetAction(
		mockinput.NewMockConsole(),
		&testConfigMgr{},
		&configResetActionFlags{force: true},
		[]string{},
	)
	_, err := a.(*configResetAction).Run(context.Background())
	require.NoError(t, err)
}

func Test_ConfigResetAction_Run_WithPathArg(t *testing.T) {
	a := newConfigResetAction(
		mockinput.NewMockConsole(),
		&testConfigMgr{},
		&configResetActionFlags{force: true},
		[]string{"defaults.subscription"},
	)
	_, err := a.(*configResetAction).Run(context.Background())
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
	_, err := a.(*configResetAction).Run(context.Background())
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
	_, err := a.(*configResetAction).Run(context.Background())
	require.NoError(t, err)
}

// ================================================================
// configListAlphaAction.Run
// ================================================================

func Test_ConfigListAlphaAction_Run(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	fm := alpha.NewFeaturesManager(&testConfigMgr{})
	a := newConfigListAlphaAction(fm, console, []string{})
	_, err := a.(*configListAlphaAction).Run(context.Background())
	require.NoError(t, err)
}

func Test_ConfigListAlphaAction_Run_WithArg(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	fm := alpha.NewFeaturesManager(&testConfigMgr{})
	a := newConfigListAlphaAction(fm, console, []string{"some-feature"})
	_, err := a.(*configListAlphaAction).Run(context.Background())
	// Toggling an unknown feature may succeed or fail
	_ = err
}

// ================================================================
// configOptionsAction.Run
// ================================================================

func Test_ConfigOptionsAction_Run(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	var buf bytes.Buffer
	a := newConfigOptionsAction(console, &output.JsonFormatter{}, &buf, &testConfigMgr{}, []string{})
	_, err := a.(*configOptionsAction).Run(context.Background())
	require.NoError(t, err)
}

// ================================================================
// selectKeyVaultSecret – deeper paths
// ================================================================

func Test_SelectKeyVaultSecret_Success(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, nil
	})

	kvSvc := &mockKvSvcForSelect{}
	kvSvc.secrets = []string{"secret-one", "secret-two"}

	action := &envSetSecretAction{
		console:   console,
		kvService: kvSvc,
	}

	secret, err := action.selectKeyVaultSecret(context.Background(), "sub-id", "my-vault")
	require.NoError(t, err)
	assert.Equal(t, "secret-one", secret)
}

func Test_SelectKeyVaultSecret_ListError(t *testing.T) {
	console := mockinput.NewMockConsole()
	kvSvc := &mockKvSvcForSelect{listErr: fmt.Errorf("list failed")}

	action := &envSetSecretAction{
		console:   console,
		kvService: kvSvc,
	}

	_, err := action.selectKeyVaultSecret(context.Background(), "sub-id", "my-vault")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing Key Vault secrets")
}

func Test_SelectKeyVaultSecret_EmptySecrets(t *testing.T) {
	console := mockinput.NewMockConsole()
	kvSvc := &mockKvSvcForSelect{secrets: []string{}}

	action := &envSetSecretAction{
		console:   console,
		kvService: kvSvc,
	}

	_, err := action.selectKeyVaultSecret(context.Background(), "sub-id", "my-vault")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Key Vault secrets were found")
}

func Test_SelectKeyVaultSecret_SelectError(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("user cancelled")
	})

	kvSvc := &mockKvSvcForSelect{secrets: []string{"s1"}}

	action := &envSetSecretAction{
		console:   console,
		kvService: kvSvc,
	}

	_, err := action.selectKeyVaultSecret(context.Background(), "sub-id", "vault")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting Key Vault secret")
}

func Test_SelectKeyVaultSecret_SecondItem(t *testing.T) {
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 1, nil
	})

	kvSvc := &mockKvSvcForSelect{secrets: []string{"first", "second", "third"}}

	action := &envSetSecretAction{
		console:   console,
		kvService: kvSvc,
	}

	secret, err := action.selectKeyVaultSecret(context.Background(), "sub", "vault")
	require.NoError(t, err)
	assert.Equal(t, "second", secret)
}

// mockKvSvcForSelect - minimal mock for selectKeyVaultSecret
type mockKvSvcForSelect struct {
	secrets []string
	listErr error
}

func (m *mockKvSvcForSelect) ListKeyVaultSecrets(ctx context.Context, subId string, vaultName string) ([]string, error) {
	return m.secrets, m.listErr
}
func (m *mockKvSvcForSelect) GetKeyVault(ctx context.Context, subId, rgName, vaultName string) (*keyvault.KeyVault, error) {
	return nil, nil
}
func (m *mockKvSvcForSelect) GetKeyVaultSecret(
	ctx context.Context, subId, vaultName, secretName string,
) (*keyvault.Secret, error) {
	return nil, nil
}
func (m *mockKvSvcForSelect) PurgeKeyVault(ctx context.Context, subId, vaultName, location string) error {
	return nil
}
func (m *mockKvSvcForSelect) ListSubscriptionVaults(
	ctx context.Context, subId string,
) ([]keyvault.Vault, error) {
	return nil, nil
}
func (m *mockKvSvcForSelect) CreateVault(
	ctx context.Context,
	tenantId, subId, rgName, location, vaultName string,
) (keyvault.Vault, error) {
	return keyvault.Vault{}, nil
}
func (m *mockKvSvcForSelect) CreateKeyVaultSecret(
	ctx context.Context,
	subId, vaultName, secretName, secretValue string,
) error {
	return nil
}
func (m *mockKvSvcForSelect) SecretFromAkvs(ctx context.Context, akvs string) (string, error) {
	return "", nil
}
func (m *mockKvSvcForSelect) SecretFromKeyVaultReference(ctx context.Context, ref, defaultSubId string) (string, error) {
	return "", nil
}

// ================================================================
// versionAction.Run
// ================================================================

func Test_VersionAction_Run(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	fm := alpha.NewFeaturesManager(&testConfigMgr{})
	console := mockinput.NewMockConsole()
	a := newVersionAction(&versionFlags{}, &output.JsonFormatter{}, &buf, console, fm)
	_, err := a.(*versionAction).Run(context.Background())
	require.NoError(t, err)
}

// ================================================================
// completionBashAction.Run etc – exercise shell completion
// ================================================================

func Test_CompletionBashAction_Run(t *testing.T) {
	t.Parallel()
	rootCmd := &cobra.Command{Use: "azd"}
	a := newCompletionBashAction(rootCmd)
	_, err := a.(*completionAction).Run(context.Background())
	require.NoError(t, err)
}

func Test_CompletionZshAction_Run(t *testing.T) {
	t.Parallel()
	rootCmd := &cobra.Command{Use: "azd"}
	a := newCompletionZshAction(rootCmd)
	_, err := a.(*completionAction).Run(context.Background())
	require.NoError(t, err)
}

func Test_CompletionFishAction_Run(t *testing.T) {
	t.Parallel()
	rootCmd := &cobra.Command{Use: "azd"}
	a := newCompletionFishAction(rootCmd)
	_, err := a.(*completionAction).Run(context.Background())
	require.NoError(t, err)
}

func Test_CompletionPowerShellAction_Run(t *testing.T) {
	t.Parallel()
	rootCmd := &cobra.Command{Use: "azd"}
	a := newCompletionPowerShellAction(rootCmd)
	_, err := a.(*completionAction).Run(context.Background())
	require.NoError(t, err)
}
