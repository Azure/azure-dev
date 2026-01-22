// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mockprompt

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/stretchr/testify/mock"
)

type MockPromptService struct {
	mock.Mock
}

func (m *MockPromptService) PromptSubscription(
	ctx context.Context,
	selectorOptions *prompt.SelectOptions,
) (*account.Subscription, error) {
	args := m.Called(ctx, selectorOptions)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*account.Subscription), args.Error(1)
}

func (m *MockPromptService) PromptLocation(
	ctx context.Context,
	azureContext *prompt.AzureContext,
	selectorOptions *prompt.SelectOptions,
) (*account.Location, error) {
	args := m.Called(ctx, azureContext, selectorOptions)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*account.Location), args.Error(1)
}

func (m *MockPromptService) PromptResourceGroup(
	ctx context.Context,
	azureContext *prompt.AzureContext,
	options *prompt.ResourceGroupOptions,
) (*azapi.ResourceGroup, error) {
	args := m.Called(ctx, azureContext, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*azapi.ResourceGroup), args.Error(1)
}

func (m *MockPromptService) PromptSubscriptionResource(
	ctx context.Context,
	azureContext *prompt.AzureContext,
	options prompt.ResourceOptions,
) (*azapi.ResourceExtended, error) {
	args := m.Called(ctx, azureContext, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*azapi.ResourceExtended), args.Error(1)
}

func (m *MockPromptService) PromptResourceGroupResource(
	ctx context.Context,
	azureContext *prompt.AzureContext,
	options prompt.ResourceOptions,
) (*azapi.ResourceExtended, error) {
	args := m.Called(ctx, azureContext, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*azapi.ResourceExtended), args.Error(1)
}
