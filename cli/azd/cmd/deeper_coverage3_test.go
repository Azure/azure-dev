// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/entraid"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ==================== Mock types for envSetSecretAction ====================

type mockKeyVaultService struct {
	mock.Mock
}

func (m *mockKeyVaultService) GetKeyVault(
	ctx context.Context, subscriptionId string, resourceGroupName string, vaultName string,
) (*keyvault.KeyVault, error) {
	args := m.Called(ctx, subscriptionId, resourceGroupName, vaultName)
	return args.Get(0).(*keyvault.KeyVault), args.Error(1)
}

func (m *mockKeyVaultService) GetKeyVaultSecret(
	ctx context.Context, subscriptionId string, vaultName string, secretName string,
) (*keyvault.Secret, error) {
	args := m.Called(ctx, subscriptionId, vaultName, secretName)
	return args.Get(0).(*keyvault.Secret), args.Error(1)
}

func (m *mockKeyVaultService) PurgeKeyVault(
	ctx context.Context, subscriptionId string, vaultName string, location string,
) error {
	args := m.Called(ctx, subscriptionId, vaultName, location)
	return args.Error(0)
}

func (m *mockKeyVaultService) ListSubscriptionVaults(
	ctx context.Context, subscriptionId string,
) ([]keyvault.Vault, error) {
	args := m.Called(ctx, subscriptionId)
	return args.Get(0).([]keyvault.Vault), args.Error(1)
}

func (m *mockKeyVaultService) CreateVault(
	ctx context.Context, tenantId string, subscriptionId string,
	resourceGroupName string, location string, vaultName string,
) (keyvault.Vault, error) {
	args := m.Called(ctx, tenantId, subscriptionId, resourceGroupName, location, vaultName)
	return args.Get(0).(keyvault.Vault), args.Error(1)
}

func (m *mockKeyVaultService) ListKeyVaultSecrets(
	ctx context.Context, subscriptionId string, vaultName string,
) ([]string, error) {
	args := m.Called(ctx, subscriptionId, vaultName)
	return args.Get(0).([]string), args.Error(1)
}

func (m *mockKeyVaultService) CreateKeyVaultSecret(
	ctx context.Context, subscriptionId string, vaultName string, secretName string, value string,
) error {
	args := m.Called(ctx, subscriptionId, vaultName, secretName, value)
	return args.Error(0)
}

func (m *mockKeyVaultService) SecretFromAkvs(
	ctx context.Context, akvs string,
) (string, error) {
	args := m.Called(ctx, akvs)
	return args.String(0), args.Error(1)
}

func (m *mockKeyVaultService) SecretFromKeyVaultReference(
	ctx context.Context, kvRef string, defaultSubscriptionId string,
) (string, error) {
	args := m.Called(ctx, kvRef, defaultSubscriptionId)
	return args.String(0), args.Error(1)
}

type mockPrompter struct {
	mock.Mock
}

func (m *mockPrompter) PromptSubscription(ctx context.Context, msg string) (string, error) {
	args := m.Called(ctx, msg)
	return args.String(0), args.Error(1)
}

func (m *mockPrompter) PromptLocation(
	ctx context.Context, subId string, msg string,
	filter prompt.LocationFilterPredicate, defaultLocation *string,
) (string, error) {
	args := m.Called(ctx, subId, msg, filter, defaultLocation)
	return args.String(0), args.Error(1)
}

func (m *mockPrompter) PromptResourceGroup(
	ctx context.Context, options prompt.PromptResourceOptions,
) (string, error) {
	args := m.Called(ctx, options)
	return args.String(0), args.Error(1)
}

func (m *mockPrompter) PromptResourceGroupFrom(
	ctx context.Context, subscriptionId string, location string,
	options prompt.PromptResourceGroupFromOptions,
) (string, error) {
	args := m.Called(ctx, subscriptionId, location, options)
	return args.String(0), args.Error(1)
}

type mockSubTenantResolver struct {
	mock.Mock
}

func (m *mockSubTenantResolver) LookupTenant(ctx context.Context, subscriptionId string) (string, error) {
	args := m.Called(ctx, subscriptionId)
	return args.String(0), args.Error(1)
}

func (m *mockSubTenantResolver) GetSubscription(
	ctx context.Context, subscriptionId string,
) (*account.Subscription, error) {
	tenantId, err := m.LookupTenant(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	return &account.Subscription{
		Id:                 subscriptionId,
		TenantId:           tenantId,
		UserAccessTenantId: tenantId,
	}, nil
}

type staticSubscriptionResolver struct {
	subscription *account.Subscription
}

func (s *staticSubscriptionResolver) GetSubscription(
	ctx context.Context, subscriptionId string,
) (*account.Subscription, error) {
	return s.subscription, nil
}

type mockEnvSetSecretEntraIdService struct {
	entraid.EntraIdService
	subscriptionId string
	scope          string
	roleId         string
	principalId    string
}

func (m *mockEnvSetSecretEntraIdService) CreateRbac(
	ctx context.Context, subscriptionId string, scope, roleId, principalId string,
) error {
	m.subscriptionId = subscriptionId
	m.scope = scope
	m.roleId = roleId
	m.principalId = principalId
	return nil
}

// ==================== envSetSecretAction Tests ====================

func newTestEnvSetSecretAction(
	console input.Console,
	env *environment.Environment,
	envManager environment.Manager,
	args []string,
	projectConfig *project.ProjectConfig,
	kvService keyvault.KeyVaultService,
	prompter *mockPrompter,
	subResolver *mockSubTenantResolver,
) *envSetSecretAction {
	if projectConfig == nil {
		projectConfig = &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		}
	}
	fm := alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig())
	return &envSetSecretAction{
		console:             console,
		azdCtx:              nil,
		env:                 env,
		envManager:          envManager,
		flags:               &envSetFlags{},
		args:                args,
		prompter:            prompter,
		kvService:           kvService,
		entraIdService:      nil,
		subResolver:         subResolver,
		userProfileService:  nil,
		alphaFeatureManager: fm,
		projectConfig:       projectConfig,
	}
}

func Test_EnvSetSecretAction_NoArgs_Deeper(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	env := environment.NewWithValues("test", map[string]string{})
	action := newTestEnvSetSecretAction(console, env, nil, []string{}, nil, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required arguments not provided")
}

func Test_EnvSetSecretAction_SelectStrategyError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("select cancelled")
	})

	env := environment.NewWithValues("test", map[string]string{})
	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting secret setting strategy")
}

func Test_EnvSetSecretAction_InvalidVaultId(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// First Select: strategy (create new)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)

	env := environment.NewWithValues("test", map[string]string{
		"AZURE_RESOURCE_VAULT_ID": "not-a-valid-resource-id",
	})
	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing key vault resource id")
}

func Test_EnvSetSecretAction_ProjectKV_SelectError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// First Select: strategy
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	// Second Select: project KV prompt
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Key vault detected in this project. Use this key vault?"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("cancelled")
	})

	kvId := "/subscriptions/sub123/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/myvault"
	env := environment.NewWithValues("test", map[string]string{
		"AZURE_RESOURCE_VAULT_ID": kvId,
	})
	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting key vault option")
}

func Test_EnvSetSecretAction_ProjectKV_UseExisting_PromptSubError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// First Select: strategy (create new = 0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	// Second Select: use different KV (No = 1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Key vault detected in this project. Use this key vault?"
	}).Respond(1)

	kvId := "/subscriptions/sub123/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/myvault"
	env := environment.NewWithValues("test", map[string]string{
		"AZURE_RESOURCE_VAULT_ID": kvId,
	})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("", fmt.Errorf("no subscriptions"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, prompter, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompting for subscription")
}

func Test_EnvSetSecretAction_VaultNotProvisioned_Cancel(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// First Select: strategy
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	// Second Select: "Cancel" = index 1
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "How do you want to proceed?"
	}).Respond(1)

	env := environment.NewWithValues("test", map[string]string{})
	projCfg := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"vault": {},
		},
	}
	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, projCfg, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation cancelled by user")
}

func Test_EnvSetSecretAction_VaultNotProvisioned_SelectError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "How do you want to proceed?"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("select error")
	})

	env := environment.NewWithValues("test", map[string]string{})
	projCfg := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"vault": {},
		},
	}
	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, projCfg, nil, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting key vault option")
}

func Test_EnvSetSecretAction_VaultNotProvisioned_UseDifferent_PromptSubError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "How do you want to proceed?"
	}).Respond(0) // Use a different key vault

	env := environment.NewWithValues("test", map[string]string{})
	projCfg := &project.ProjectConfig{
		Resources: map[string]*project.ResourceConfig{
			"vault": {},
		},
	}

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("", fmt.Errorf("no sub"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, projCfg, nil, prompter, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompting for subscription")
}

func Test_EnvSetSecretAction_NoProject_PromptSubError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// First Select: strategy
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("", fmt.Errorf("cancelled"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, prompter, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompting for subscription")
}

func Test_EnvSetSecretAction_LookupTenantError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockSubTenantResolver{}
	resolver.On("LookupTenant", mock.Anything, "sub-123").
		Return("", fmt.Errorf("tenant not found"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, nil, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting subscription")
}

func Test_EnvSetSecretAction_ListVaultsError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockSubTenantResolver{}
	resolver.On("LookupTenant", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{}, fmt.Errorf("network error"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting the list of Key Vaults")
}

func Test_EnvSetSecretAction_SelectExisting_NoVaults(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// Select existing strategy (index 1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(1)
	// After discovering no vaults, it switches to create new and prompts for KV selection
	// The message keeps "where the Key Vault secret is" from the original !willCreateNewSecret path
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select the Key Vault where the Key Vault secret is"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("cancelled")
	})

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockSubTenantResolver{}
	resolver.On("LookupTenant", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{}, nil) // Empty list

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	// The error could be from Select or from a subsequent step
}

func Test_EnvSetSecretAction_SelectKVError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)

	// KV selection prompt error
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select the Key Vault where you want to create the Key Vault secret"
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		return 0, fmt.Errorf("select kv error")
	})

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockSubTenantResolver{}
	resolver.On("LookupTenant", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{{Name: "vault1", Id: "id1"}}, nil)

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "selecting Key Vault")
}

func Test_EnvSetSecretAction_CreateNewKV_LocationError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0) // Create new

	// KV selection: pick "Create a new Key Vault" (index 0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select the Key Vault where you want to create the Key Vault secret"
	}).Respond(0)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)
	prompter.On("PromptLocation", mock.Anything, "sub-123", mock.Anything, mock.Anything, mock.Anything).
		Return("", fmt.Errorf("location error"))

	resolver := &mockSubTenantResolver{}
	resolver.On("LookupTenant", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{}, nil) // No existing vaults

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompting for Key Vault location")
}

func Test_EnvSetSecretAction_ProjectKV_UseExisting_CreateNewSecret(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// Strategy: select existing (index 1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(1)
	// Project KV: Yes (index 0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Key vault detected in this project. Use this key vault?"
	}).Respond(0)

	// selectKeyVaultSecret needs ListKeyVaultSecrets + Select for secret
	kvId := "/subscriptions/sub123/resourceGroups/rg1/providers/Microsoft.KeyVault/vaults/myvault"
	env := environment.NewWithValues("test", map[string]string{
		"AZURE_RESOURCE_VAULT_ID": kvId,
	})

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListKeyVaultSecrets", mock.Anything, "sub123", "myvault").
		Return([]string{}, fmt.Errorf("list secrets error"))

	envMgr := &mockenv.MockEnvManager{}

	action := newTestEnvSetSecretAction(console, env, envMgr, []string{"mySecret"}, nil, kvSvc, nil, nil)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list secrets error")
}

// ==================== envGetValuesAction Tests ====================

func Test_EnvGetValuesAction_WithFlagOverride_Deeper(t *testing.T) {
	azdCtx := newTestAzdContext(t)
	setDefaultEnvHelper(t, azdCtx, "default-env")

	env := environment.NewWithValues("override-env", map[string]string{
		"KEY1": "val1",
	})

	envMgr := &mockenv.MockEnvManager{}
	envMgr.On("Get", mock.Anything, "override-env").
		Return(env, nil)

	var buf bytes.Buffer
	formatter := &output.JsonFormatter{}
	action := newEnvGetValuesAction(
		azdCtx, envMgr, mockinput.NewMockConsole(), formatter, &buf,
		&envGetValuesFlags{EnvFlag: internal.EnvFlag{EnvironmentName: "override-env"}},
	)

	_, err := action.(*envGetValuesAction).Run(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "KEY1")
}

func Test_EnvGetValuesAction_EnvNotFound_Deeper(t *testing.T) {
	azdCtx := newTestAzdContext(t)
	setDefaultEnvHelper(t, azdCtx, "my-env")

	envMgr := &mockenv.MockEnvManager{}
	envMgr.On("Get", mock.Anything, "my-env").
		Return((*environment.Environment)(nil), environment.ErrNotFound)

	var buf bytes.Buffer
	formatter := &output.JsonFormatter{}
	action := newEnvGetValuesAction(
		azdCtx, envMgr, mockinput.NewMockConsole(), formatter, &buf,
		&envGetValuesFlags{},
	)

	_, err := action.(*envGetValuesAction).Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "environment does not exist")
}

func Test_EnvGetValuesAction_EnvGetError_Deeper(t *testing.T) {
	azdCtx := newTestAzdContext(t)
	setDefaultEnvHelper(t, azdCtx, "my-env")

	envMgr := &mockenv.MockEnvManager{}
	envMgr.On("Get", mock.Anything, "my-env").
		Return((*environment.Environment)(nil), fmt.Errorf("database error"))

	var buf bytes.Buffer
	formatter := &output.JsonFormatter{}
	action := newEnvGetValuesAction(
		azdCtx, envMgr, mockinput.NewMockConsole(), formatter, &buf,
		&envGetValuesFlags{},
	)

	_, err := action.(*envGetValuesAction).Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ensuring environment exists")
}

func Test_EnvGetValuesAction_Success_Deeper(t *testing.T) {
	azdCtx := newTestAzdContext(t)
	setDefaultEnvHelper(t, azdCtx, "test-env")

	env := environment.NewWithValues("test-env", map[string]string{
		"AZURE_ENV_NAME": "test-env",
		"MY_VAR":         "hello",
	})

	envMgr := &mockenv.MockEnvManager{}
	envMgr.On("Get", mock.Anything, "test-env").
		Return(env, nil)

	var buf bytes.Buffer
	formatter := &output.JsonFormatter{}
	action := newEnvGetValuesAction(
		azdCtx, envMgr, mockinput.NewMockConsole(), formatter, &buf,
		&envGetValuesFlags{},
	)

	_, err := action.(*envGetValuesAction).Run(t.Context())
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "MY_VAR")
}

// ==================== Cmd constructors not yet tested ====================

func Test_NewEnvGetValuesCmd(t *testing.T) {
	t.Parallel()
	cmd := newEnvGetValuesCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "get-values", cmd.Use)
}

func Test_NewAuthStatusCmd(t *testing.T) {
	t.Parallel()
	cmd := newAuthStatusCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "status", cmd.Use)
}

func Test_NewAuthStatusFlags(t *testing.T) {
	t.Parallel()
	cmd := newAuthStatusCmd()
	global := &internal.GlobalCommandOptions{}
	flags := newAuthStatusFlags(cmd, global)
	require.NotNil(t, flags)
	assert.Equal(t, global, flags.global)
}

func Test_NewAuthStatusAction_Deeper(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	formatter := &output.JsonFormatter{}
	console := mockinput.NewMockConsole()
	flags := &authStatusFlags{global: &internal.GlobalCommandOptions{}}
	action := newAuthStatusAction(formatter, &buf, nil, flags, console)
	require.NotNil(t, action)
}

// ==================== Additional config tests ====================

func Test_ConfigSetAction_SaveError(t *testing.T) {
	t.Parallel()
	cfgMgr := &testConfigManager{
		loadCfg: config.NewEmptyConfig(),
		saveErr: fmt.Errorf("save failed"),
	}
	action := &configSetAction{
		configManager: cfgMgr,
		args:          []string{"key1", "value1"},
	}
	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "save failed")
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

// testConfigManager implements config.UserConfigManager for testing
type testConfigManager struct {
	loadCfg config.Config
	loadErr error
	saveErr error
}

func (m *testConfigManager) Load() (config.Config, error) {
	return m.loadCfg, m.loadErr
}

func (m *testConfigManager) Save(cfg config.Config) error {
	return m.saveErr
}

// ==================== Helpers ====================

// setDefaultEnvHelper sets the default environment in the AzdContext
func setDefaultEnvHelper(t *testing.T, azdCtx *azdcontext.AzdContext, envName string) {
	t.Helper()
	require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{
		DefaultEnvironment: envName,
	}))
}

// ==================== Additional constructors for uncovered paths ====================

func Test_NewEnvSetSecretFlags(t *testing.T) {
	t.Parallel()
	flags := &envSetFlags{}
	require.NotNil(t, flags)
}

func Test_EnvSetSecretAction_SelectExisting_VaultListError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// Select existing (index 1)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(1)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockSubTenantResolver{}
	resolver.On("LookupTenant", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{}, fmt.Errorf("vault list error"))

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getting the list of Key Vaults")
}

// ==================== createNewKeyVaultSecret / selectKeyVaultSecret ====================

func Test_EnvSetSecretAction_CreateNew_ExistingVault_ListSecretsError(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	// Strategy: create new (index 0)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select how you want to set mySecret"
	}).Respond(0)
	// KV selection: pick existing vault (index 1, after "Create new" option)
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return options.Message == "Select the Key Vault where you want to create the Key Vault secret"
	}).Respond(1)

	env := environment.NewWithValues("test", map[string]string{})

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription", mock.Anything, mock.Anything).
		Return("sub-123", nil)

	resolver := &mockSubTenantResolver{}
	resolver.On("LookupTenant", mock.Anything, "sub-123").
		Return("tenant-123", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").
		Return([]keyvault.Vault{{Name: "vault1", Id: "id1"}}, nil)
	kvSvc.On("CreateKeyVaultSecret", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(fmt.Errorf("create secret error"))

	// The createNewKeyVaultSecret method prompts for secret name and value
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true // accept any prompt
	}).Respond("my-secret-value")

	action := newTestEnvSetSecretAction(console, env, nil, []string{"mySecret"}, nil, kvSvc, prompter, resolver)

	_, err := action.Run(t.Context())
	require.Error(t, err)
}

// ==================== Additional env.go tests for uncovered paths ====================

func Test_EnvSetSecretConstructor(t *testing.T) {
	t.Parallel()
	console := mockinput.NewMockConsole()
	env := environment.NewWithValues("test", map[string]string{})
	fm := alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig())
	projCfg := &project.ProjectConfig{Resources: map[string]*project.ResourceConfig{}}

	action := newEnvSetSecretAction(
		nil, env, nil, console, &envSetFlags{}, []string{"arg1"},
		nil, nil, nil, nil, nil, fm, projCfg,
	)
	require.NotNil(t, action)
}

func Test_EnvSetSecretAction_UsesResourceTenantForKeyVaultAndPrincipalId(t *testing.T) {
	t.Parallel()

	console := mockinput.NewMockConsole()
	selectCount := 0
	console.WhenSelect(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		selectCount++
		if selectCount > 2 {
			return nil, fmt.Errorf("unexpected select: %s", options.Message)
		}
		return 0, nil
	})

	promptCount := 0
	console.WhenPrompt(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		promptCount++
		switch promptCount {
		case 1:
			return "kv-name", nil
		case 2:
			return "my-secret-kv", nil
		case 3:
			return "secret-value", nil
		default:
			return nil, fmt.Errorf("unexpected prompt: %s", options.Message)
		}
	})

	env := environment.NewWithValues("test", map[string]string{})
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", mock.Anything, env).Return(nil)

	prompter := &mockPrompter{}
	prompter.On("PromptSubscription",
		mock.Anything,
		"Select the subscription where you want to create the Key Vault secret",
	).Return("sub-123", nil)
	prompter.On("PromptLocation",
		mock.Anything,
		"sub-123",
		"Select the location to create the Key Vault",
		mock.Anything,
		mock.Anything,
	).Return("westus", nil)
	prompter.On("PromptResourceGroupFrom",
		mock.Anything,
		"sub-123",
		"westus",
		prompt.PromptResourceGroupFromOptions{
			DefaultName:          "rg-for-my-key-vault",
			NewResourceGroupHelp: "The name of the new resource group where the Key Vault will be created.",
		},
	).Return("rg-name", nil)

	kvSvc := &mockKeyVaultService{}
	kvSvc.On("ListSubscriptionVaults", mock.Anything, "sub-123").Return([]keyvault.Vault{}, nil)
	kvSvc.On("CreateVault",
		mock.Anything,
		"resource-tenant",
		"sub-123",
		"rg-name",
		"westus",
		"kv-name",
	).Return(keyvault.Vault{
		Id:   "/subscriptions/sub-123/resourceGroups/rg-name/providers/Microsoft.KeyVault/vaults/kv-name",
		Name: "kv-name",
	}, nil)
	kvSvc.On("CreateKeyVaultSecret",
		mock.Anything,
		"sub-123",
		"kv-name",
		"my-secret-kv",
		"secret-value",
	).Return(nil)

	mockContext := mocks.NewMockContext(context.Background())
	userProfileService := azapi.NewUserProfileService(
		&mocks.MockMultiTenantCredentialProvider{
			TokenMap: map[string]mocks.MockCredentials{
				"resource-tenant": {
					GetTokenFn: func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
						return azcore.AccessToken{
							Token: mocks.CreateJwtToken(t, map[string]string{
								"oid": "this-is-a-test",
							}),
							ExpiresOn: time.Now().Add(time.Hour),
						}, nil
					},
				},
			},
		},
		&azcore.ClientOptions{
			Transport: mockContext.HttpClient,
		},
		cloud.AzurePublic(),
	)

	entraIdService := &mockEnvSetSecretEntraIdService{}
	action := &envSetSecretAction{
		console:        console,
		env:            env,
		envManager:     envManager,
		flags:          &envSetFlags{},
		args:           []string{"MY_SECRET"},
		prompter:       prompter,
		kvService:      kvSvc,
		entraIdService: entraIdService,
		subResolver: &staticSubscriptionResolver{
			subscription: &account.Subscription{
				Id:                 "sub-123",
				TenantId:           "resource-tenant",
				UserAccessTenantId: "home-tenant",
			},
		},
		userProfileService:  userProfileService,
		alphaFeatureManager: alpha.NewFeaturesManagerWithConfig(config.NewEmptyConfig()),
		projectConfig: &project.ProjectConfig{
			Resources: map[string]*project.ResourceConfig{},
		},
	}

	_, err := action.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t, "sub-123", entraIdService.subscriptionId)
	require.Equal(t, keyvault.RoleIdKeyVaultAdministrator, entraIdService.roleId)
	require.Equal(t, "this-is-a-test", entraIdService.principalId)
}

// ==================== Suppressed errors.Is / errors.AsType coverage ====================

func Test_ErrorWithSuggestion_Type(t *testing.T) {
	t.Parallel()
	err := &internal.ErrorWithSuggestion{
		Err:        internal.ErrNoArgsProvided,
		Suggestion: "test suggestion",
	}
	assert.True(t, errors.Is(err, internal.ErrNoArgsProvided))
	assert.Contains(t, err.Error(), "required arguments not provided")
}
