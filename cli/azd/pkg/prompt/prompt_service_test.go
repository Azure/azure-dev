// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockauth"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// inMemoryUserConfigManager is an in-memory implementation of config.UserConfigManager for tests,
// avoiding reads from the real user config file on disk.
type inMemoryUserConfigManager struct {
	cfg config.Config
}

func newInMemoryUserConfigManager(cfg config.Config) *inMemoryUserConfigManager {
	if cfg == nil {
		cfg = config.NewEmptyConfig()
	}
	return &inMemoryUserConfigManager{cfg: cfg}
}

func (m *inMemoryUserConfigManager) Load() (config.Config, error) { return m.cfg, nil }
func (m *inMemoryUserConfigManager) Save(_ config.Config) error   { return nil }

func Test_PromptService_PromptSubscription(t *testing.T) {
	ucm := newInMemoryUserConfigManager(nil)

	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}
	mockConsole := mockinput.NewMockConsole()

	tokenClaims := auth.TokenClaims{
		TenantId: "tenant-1",
	}

	authManager.
		On("ClaimsForCurrentUser", mock.Anything, mock.Anything).
		Return(tokenClaims, nil)

	subscriptionManager.
		On("GetSubscriptions", mock.Anything, tokenClaims.TenantId).
		Return([]account.Subscription{
			{
				Id:       "/subscriptions/subscription-1",
				TenantId: "tenant-1",
				Name:     "Subscription 1",
			},
			{
				Id:       "/subscriptions/subscription-2",
				TenantId: "tenant-2",
				Name:     "Subscription 2",
			},
		}, nil)

	globalOptions := &internal.GlobalCommandOptions{
		NoPrompt: false,
	}

	promptService := NewPromptService(
		authManager, mockConsole, ucm, subscriptionManager, resourceService, globalOptions,
	)
	require.NotNil(t, promptService)
}

func TestFormatSubscriptionDisplayName_DemoModeHidesId(t *testing.T) {
	displayName := formatSubscriptionDisplayName(&account.Subscription{
		Id:   "/subscriptions/sub-1",
		Name: "Subscription 1",
	}, true)

	require.Equal(t, "Subscription 1", displayName)
}

func TestPromptSubscription_NoPrompt_ReturnsPromptRequiredError(t *testing.T) {
	ucm := newInMemoryUserConfigManager(nil)
	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}
	mockConsole := mockinput.NewMockConsole()
	mockConsole.SetNoPromptMode(true)

	subscriptionManager.
		On("GetSubscriptions", mock.Anything).
		Return([]account.Subscription{
			{Id: "sub-1", TenantId: "tenant-1", Name: "My Only Sub"},
		}, nil)

	ps := NewPromptService(
		authManager,
		mockConsole,
		ucm,
		subscriptionManager,
		resourceService,
		&internal.GlobalCommandOptions{NoPrompt: true},
	)

	_, err := ps.PromptSubscription(context.Background(), nil)
	requirePromptRequiredError(t, err, input.RequiredInput{
		Name: "subscription",
		Sources: []input.InputSource{
			{
				Kind: input.InputSourceEnvironment,
				Name: environment.SubscriptionIdEnvVarName,
			},
		},
	})
}

func TestPromptLocation_NoPrompt_ReturnsPromptRequiredError(t *testing.T) {
	ucm := newInMemoryUserConfigManager(nil)
	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}
	mockConsole := mockinput.NewMockConsole()
	mockConsole.SetNoPromptMode(true)

	subscriptionManager.
		On("GetSubscriptions", mock.Anything).
		Return([]account.Subscription{
			{Id: "sub-1", TenantId: "tenant-1", Name: "Sub 1"},
		}, nil)

	ps := NewPromptService(
		authManager,
		mockConsole,
		ucm,
		subscriptionManager,
		resourceService,
		&internal.GlobalCommandOptions{NoPrompt: true},
	)

	_, err := ps.PromptLocation(context.Background(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-123"},
	}, nil)
	requirePromptRequiredError(t, err, input.RequiredInput{
		Name: "location",
		Sources: []input.InputSource{
			{
				Kind: input.InputSourceEnvironment,
				Name: environment.LocationEnvVarName,
			},
		},
	})
}

func TestPromptResourceGroup_NoPrompt_ReturnsPromptRequiredError(t *testing.T) {
	ucm := newInMemoryUserConfigManager(nil)
	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}
	mockConsole := mockinput.NewMockConsole()
	mockConsole.SetNoPromptMode(true)

	subscriptionManager.
		On("GetLocations", mock.Anything, "sub-123").
		Return([]account.Location{
			{Name: "eastus2", DisplayName: "East US 2", RegionalDisplayName: "(US) East US 2"},
			{Name: "westus3", DisplayName: "West US 3", RegionalDisplayName: "(US) West US 3"},
		}, nil)

	ps := NewPromptService(
		authManager,
		mockConsole,
		ucm,
		subscriptionManager,
		resourceService,
		&internal.GlobalCommandOptions{NoPrompt: true},
	)

	_, err := ps.PromptResourceGroup(context.Background(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-123"},
	}, nil)
	requirePromptRequiredError(t, err, input.RequiredInput{
		Name: "resource group",
		Sources: []input.InputSource{
			{
				Kind: input.InputSourceEnvironment,
				Name: environment.ResourceGroupEnvVarName,
			},
		},
	})
}

func TestPromptSubscriptionResource_NoPrompt_ReturnsPromptRequiredError(t *testing.T) {
	ucm := newInMemoryUserConfigManager(nil)
	authManager := &mockauth.MockAuthManager{}
	subscriptionManager := &mockaccount.MockSubscriptionManager{}
	resourceService := &mockazapi.MockResourceService{}
	mockConsole := mockinput.NewMockConsole()
	mockConsole.SetNoPromptMode(true)

	ps := NewPromptService(
		authManager,
		mockConsole,
		ucm,
		subscriptionManager,
		resourceService,
		&internal.GlobalCommandOptions{NoPrompt: true},
	)

	_, err := ps.PromptSubscriptionResource(context.Background(), &AzureContext{
		Scope: AzureScope{SubscriptionId: "sub-123"},
	}, ResourceOptions{
		ResourceTypeDisplayName: "OpenAI account",
	})
	requirePromptRequiredError(t, err, input.RequiredInput{
		Name:        "OpenAI account",
		Description: "OpenAI account must be selected to continue.",
	})
}

func requirePromptRequiredError(
	t *testing.T,
	err error,
	expectedInput input.RequiredInput,
) *input.PromptRequiredError {
	t.Helper()

	promptErr, ok := errors.AsType[*input.PromptRequiredError](err)
	require.True(t, ok)
	require.Equal(t, []input.RequiredInput{expectedInput}, promptErr.Inputs)

	return promptErr
}
