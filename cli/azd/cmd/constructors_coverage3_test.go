// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	internalcmd "github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Extension constructors – extension.go
// ---------------------------------------------------------------------------

func Test_NewExtensionListAction(t *testing.T) {
	t.Parallel()
	action := newExtensionListAction(
		&extensionListFlags{},
		&output.JsonFormatter{},
		mockinput.NewMockConsole(),
		&bytes.Buffer{},
		nil, // sourceManager
		nil, // extensionManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionShowAction(t *testing.T) {
	t.Parallel()
	action := newExtensionShowAction(
		[]string{"test-ext"},
		&extensionShowFlags{},
		mockinput.NewMockConsole(),
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // extensionManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionInstallAction(t *testing.T) {
	t.Parallel()
	action := newExtensionInstallAction(
		[]string{"test-ext"},
		&extensionInstallFlags{},
		mockinput.NewMockConsole(),
		nil, // extensionManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionUninstallAction(t *testing.T) {
	t.Parallel()
	action := newExtensionUninstallAction(
		[]string{"test-ext"},
		&extensionUninstallFlags{},
		mockinput.NewMockConsole(),
		nil, // extensionManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionUpgradeAction(t *testing.T) {
	t.Parallel()
	action := newExtensionUpgradeAction(
		[]string{"test-ext"},
		&extensionUpgradeFlags{},
		mockinput.NewMockConsole(),
		nil, // extensionManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionSourceListAction(t *testing.T) {
	t.Parallel()
	action := newExtensionSourceListAction(
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // sourceManager
	)
	require.NotNil(t, action)
}

func Test_NewExtensionSourceAddAction(t *testing.T) {
	t.Parallel()
	action := newExtensionSourceAddAction(
		&extensionSourceAddFlags{},
		mockinput.NewMockConsole(),
		nil, // sourceManager
		[]string{"my-source"},
	)
	require.NotNil(t, action)
}

func Test_NewExtensionSourceRemoveAction(t *testing.T) {
	t.Parallel()
	action := newExtensionSourceRemoveAction(
		nil, // sourceManager
		mockinput.NewMockConsole(),
		[]string{"my-source"},
	)
	require.NotNil(t, action)
}

func Test_NewExtensionSourceValidateAction(t *testing.T) {
	t.Parallel()
	action := newExtensionSourceValidateAction(
		[]string{"my-source"},
		&extensionSourceValidateFlags{},
		mockinput.NewMockConsole(),
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // sourceManager
	)
	require.NotNil(t, action)
}

// ---------------------------------------------------------------------------
// Auth constructors – auth_login.go, auth_logout.go, auth_status.go
// ---------------------------------------------------------------------------

func Test_NewAuthLoginAction(t *testing.T) {
	t.Parallel()
	action := newAuthLoginAction(
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // authManager
		nil, // accountSubManager
		&authLoginFlags{},
		mockinput.NewMockConsole(),
		CmdAnnotations{},
		nil, // commandRunner
	)
	require.NotNil(t, action)
}

func Test_NewLogoutAction(t *testing.T) {
	t.Parallel()
	action := newLogoutAction(
		nil, // authManager
		nil, // accountSubManager
		&output.JsonFormatter{},
		&bytes.Buffer{},
		mockinput.NewMockConsole(),
		CmdAnnotations{},
	)
	require.NotNil(t, action)
}

func Test_NewAuthStatusAction(t *testing.T) {
	t.Parallel()
	action := newAuthStatusAction(
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // authManager
		&authStatusFlags{},
		mockinput.NewMockConsole(),
	)
	require.NotNil(t, action)
}

// ---------------------------------------------------------------------------
// Hooks constructor – hooks.go
// ---------------------------------------------------------------------------

func Test_NewHooksRunAction(t *testing.T) {
	t.Parallel()
	action := newHooksRunAction(
		&project.ProjectConfig{},
		nil, // importManager
		environment.NewWithValues("test", nil),
		nil, // envManager
		nil, // commandRunner
		mockinput.NewMockConsole(),
		&hooksRunFlags{},
		[]string{"pre-provision"},
		ioc.NewNestedContainer(nil),
	)
	require.NotNil(t, action)
}

// ---------------------------------------------------------------------------
// MCP constructor – mcp.go
// ---------------------------------------------------------------------------

func Test_NewMcpStartAction(t *testing.T) {
	t.Parallel()
	action := newMcpStartAction(
		&mcpStartFlags{},
		nil, // userConfigManager
		nil, // extensionManager
		nil, // grpcServer
	)
	require.NotNil(t, action)
}

// ---------------------------------------------------------------------------
// Init constructor – init.go
// ---------------------------------------------------------------------------

func Test_NewInitAction(t *testing.T) {
	t.Parallel()
	action := newInitAction(
		nil, // lazyAzdCtx
		nil, // lazyEnvManager
		nil, // cmdRun
		mockinput.NewMockConsole(),
		nil, // gitCli
		&initFlags{},
		nil, // repoInitializer
		nil, // templateManager
		nil, // featuresManager
		nil, // extensionsManager
		nil, // azd
		nil, // agentFactory
		nil, // consentManager
		nil, // configManager
	)
	require.NotNil(t, action)
}

// ---------------------------------------------------------------------------
// Update constructor – update.go
// ---------------------------------------------------------------------------

func Test_NewUpdateAction(t *testing.T) {
	t.Parallel()
	action := newUpdateAction(
		&updateFlags{},
		mockinput.NewMockConsole(),
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // configManager
		nil, // commandRunner
		nil, // alphaFeatureManager
	)
	require.NotNil(t, action)
}

// ---------------------------------------------------------------------------
// Infra constructors – infra_create.go, infra_delete.go
// ---------------------------------------------------------------------------

func Test_NewInfraCreateAction(t *testing.T) {
	t.Parallel()
	provision := &internalcmd.ProvisionAction{}
	action := newInfraCreateAction(
		&infraCreateFlags{},
		provision,
		mockinput.NewMockConsole(),
	)
	require.NotNil(t, action)
}

func Test_NewInfraDeleteAction(t *testing.T) {
	t.Parallel()
	down := &downAction{
		flags: &downFlags{},
	}
	action := newInfraDeleteAction(
		&infraDeleteFlags{},
		down,
		mockinput.NewMockConsole(),
	)
	require.NotNil(t, action)
}

// ---------------------------------------------------------------------------
// Env additional constructors – env.go
// ---------------------------------------------------------------------------

func Test_NewEnvNewAction(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	action := newEnvNewAction(
		azdCtx,
		nil, // envManager
		&envNewFlags{},
		[]string{"my-new-env"},
		mockinput.NewMockConsole(),
	)
	require.NotNil(t, action)
}

func Test_NewEnvRefreshAction(t *testing.T) {
	t.Parallel()
	action := newEnvRefreshAction(
		nil, // provisionManager
		&project.ProjectConfig{},
		nil, // projectManager
		environment.NewWithValues("test", nil),
		nil, // envManager
		nil, // prompters
		&envRefreshFlags{},
		mockinput.NewMockConsole(),
		&output.JsonFormatter{},
		&bytes.Buffer{},
		nil, // importManager
		nil, // alphaFeatureManager
	)
	require.NotNil(t, action)
}

func Test_NewEnvSetSecretAction(t *testing.T) {
	t.Parallel()
	azdCtx := newTestAzdContext(t)
	action := newEnvSetSecretAction(
		azdCtx,
		environment.NewWithValues("test", nil),
		nil, // envManager
		mockinput.NewMockConsole(),
		&envSetFlags{},
		nil, // args
		nil, // prompter
		nil, // kvService
		nil, // entraIdService
		nil, // subResolver
		nil, // userProfileService
		nil, // alphaFeatureManager
		nil, // projectConfig
	)
	require.NotNil(t, action)
}

// ---------------------------------------------------------------------------
// Consent constructors – copilot.go
// ---------------------------------------------------------------------------

func Test_NewCopilotConsentListAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentListAction(
		&copilotConsentListFlags{},
		&output.JsonFormatter{},
		&bytes.Buffer{},
		mockinput.NewMockConsole(),
		nil, // userConfigManager
		nil, // consentManager
	)
	require.NotNil(t, action)
}

func Test_NewCopilotConsentGrantAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentGrantAction(
		&copilotConsentGrantFlags{},
		mockinput.NewMockConsole(),
		nil, // userConfigManager
		nil, // consentManager
	)
	require.NotNil(t, action)
}

func Test_NewCopilotConsentRevokeAction(t *testing.T) {
	t.Parallel()
	action := newCopilotConsentRevokeAction(
		&copilotConsentRevokeFlags{},
		mockinput.NewMockConsole(),
		nil, // userConfigManager
		nil, // consentManager
	)
	require.NotNil(t, action)
}

// ---------------------------------------------------------------------------
// Alpha feature manager construction
// ---------------------------------------------------------------------------

func Test_NewAlphaFeatureManagerConfig(t *testing.T) {
	t.Parallel()
	cfg := config.NewEmptyConfig()
	fm := alpha.NewFeaturesManagerWithConfig(cfg)
	require.NotNil(t, fm)
}

// ---------------------------------------------------------------------------
// Consent manager type assertions
// ---------------------------------------------------------------------------

func Test_ConsentTypes(t *testing.T) {
	t.Parallel()
	// Verify consent type constants
	require.Equal(t, consent.ActionType("readonly"), consent.ActionReadOnly)
	require.Equal(t, consent.ActionType("any"), consent.ActionAny)
	require.Equal(t, consent.OperationType("tool"), consent.OperationTypeTool)
	require.Equal(t, consent.OperationType("sampling"), consent.OperationTypeSampling)
	require.Equal(t, consent.OperationType("elicitation"), consent.OperationTypeElicitation)
	require.Equal(t, consent.Permission("allow"), consent.PermissionAllow)
	require.Equal(t, consent.Permission("deny"), consent.PermissionDeny)
	require.Equal(t, consent.Permission("prompt"), consent.PermissionPrompt)
	require.Equal(t, consent.Scope("global"), consent.ScopeGlobal)
}

func Test_ConsentParsers(t *testing.T) {
	t.Parallel()

	t.Run("ParseActionType", func(t *testing.T) {
		t.Parallel()
		at, err := consent.ParseActionType("all")
		require.NoError(t, err)
		require.Equal(t, consent.ActionAny, at)

		at, err = consent.ParseActionType("readonly")
		require.NoError(t, err)
		require.Equal(t, consent.ActionReadOnly, at)

		_, err = consent.ParseActionType("invalid")
		require.Error(t, err)
	})

	t.Run("ParseOperationType", func(t *testing.T) {
		t.Parallel()
		ot, err := consent.ParseOperationType("tool")
		require.NoError(t, err)
		require.Equal(t, consent.OperationTypeTool, ot)

		_, err = consent.ParseOperationType("invalid")
		require.Error(t, err)
	})

	t.Run("ParsePermission", func(t *testing.T) {
		t.Parallel()
		p, err := consent.ParsePermission("allow")
		require.NoError(t, err)
		require.Equal(t, consent.PermissionAllow, p)

		_, err = consent.ParsePermission("invalid")
		require.Error(t, err)
	})

	t.Run("ParseScope", func(t *testing.T) {
		t.Parallel()
		s, err := consent.ParseScope("global")
		require.NoError(t, err)
		require.Equal(t, consent.ScopeGlobal, s)

		s, err = consent.ParseScope("project")
		require.NoError(t, err)
		require.Equal(t, consent.Scope("project"), s)

		_, err = consent.ParseScope("invalid")
		require.Error(t, err)
	})
}

func Test_ConsentTargets(t *testing.T) {
	t.Parallel()
	gt := consent.NewGlobalTarget()
	require.NotEmpty(t, gt)

	st := consent.NewServerTarget("my-server")
	require.NotEmpty(t, st)

	tt := consent.NewToolTarget("my-server", "my-tool")
	require.NotEmpty(t, tt)
}
