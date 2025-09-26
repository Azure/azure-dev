// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package prompt

import (
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	mockContextType          = mock.Anything
	mockSelectOptionsType    = mock.AnythingOfType("*prompt.SelectOptions")
	mockAzureContextType     = mock.AnythingOfType("*prompt.AzureContext")
	mockResourceGroupOptions = mock.AnythingOfType("*prompt.ResourceGroupOptions")
)

func TestAzureContext_EnsureSubscription(t *testing.T) {
	mockPromptService := &MockPromptService{}
	azureContext := NewAzureContext(mockPromptService, AzureScope{}, nil)

	mockPromptService.On("PromptSubscription", mockContextType, mockSelectOptionsType).
		Return(&account.Subscription{
			Id:       "test-subscription-id",
			TenantId: "test-tenant-id",
		}, nil)

	err := azureContext.EnsureSubscription(context.Background())
	require.NoError(t, err)
	require.Equal(t, "test-subscription-id", azureContext.Scope.SubscriptionId)
	require.Equal(t, "test-tenant-id", azureContext.Scope.TenantId)

	mockPromptService.AssertCalled(t, "PromptSubscription", mockContextType, mockSelectOptionsType)
}

func TestAzureContext_EnsureSubscription_NoPrompt(t *testing.T) {
	mockPromptService := &MockPromptService{}
	azureContext := NewAzureContext(mockPromptService, AzureScope{
		SubscriptionId: "test-subscription-id",
		TenantId:       "test-tenant-id",
	}, nil)

	err := azureContext.EnsureSubscription(context.Background())
	require.NoError(t, err)
	require.Equal(t, "test-subscription-id", azureContext.Scope.SubscriptionId)
	require.Equal(t, "test-tenant-id", azureContext.Scope.TenantId)

	mockPromptService.AssertNotCalled(t, "PromptSubscription", mock.Anything, mock.Anything)
}

func TestAzureContext_EnsureSubscription_Error(t *testing.T) {
	mockPromptService := &MockPromptService{}
	azureContext := NewAzureContext(mockPromptService, AzureScope{}, nil)

	mockPromptService.On("PromptSubscription", mockContextType, mockSelectOptionsType).
		Return(nil, fmt.Errorf("subscription error"))

	err := azureContext.EnsureSubscription(context.Background())
	require.Error(t, err)
	require.Equal(t, "", azureContext.Scope.SubscriptionId)
	require.Equal(t, "", azureContext.Scope.TenantId)
}

func TestAzureContext_EnsureResourceGroup(t *testing.T) {
	mockPromptService := &MockPromptService{}
	azureContext := NewAzureContext(mockPromptService, AzureScope{
		SubscriptionId: "test-subscription-id",
	}, nil)

	mockPromptService.On("PromptResourceGroup", mockContextType, mockAzureContextType, mockResourceGroupOptions).
		Return(&azapi.ResourceGroup{
			Name: "test-resource-group",
		}, nil)

	err := azureContext.EnsureResourceGroup(context.Background())
	require.NoError(t, err)
	require.Equal(t, "test-resource-group", azureContext.Scope.ResourceGroup)

	mockPromptService.AssertCalled(t, "PromptResourceGroup", mockContextType, mockAzureContextType, mockResourceGroupOptions)
}

func TestAzureContext_EnsureResourceGroup_NoPrompt(t *testing.T) {
	mockPromptService := &MockPromptService{}
	azureContext := NewAzureContext(mockPromptService, AzureScope{
		SubscriptionId: "test-subscription-id",
		ResourceGroup:  "test-resource-group",
	}, nil)

	err := azureContext.EnsureResourceGroup(context.Background())
	require.NoError(t, err)
	require.Equal(t, "test-resource-group", azureContext.Scope.ResourceGroup)

	mockPromptService.AssertNotCalled(t, "PromptResourceGroup", mock.Anything, mock.Anything, mock.Anything)
}

func TestAzureContext_EnsureResourceGroup_Error(t *testing.T) {
	mockPromptService := &MockPromptService{}
	azureContext := NewAzureContext(mockPromptService, AzureScope{
		SubscriptionId: "test-subscription-id",
	}, nil)

	mockPromptService.On("PromptResourceGroup", mockContextType, mockAzureContextType, mockResourceGroupOptions).
		Return(nil, fmt.Errorf("resource group error"))

	err := azureContext.EnsureResourceGroup(context.Background())
	require.Error(t, err)
	require.Equal(t, "", azureContext.Scope.ResourceGroup)
}

func TestAzureContext_EnsureLocation(t *testing.T) {
	mockPromptService := &MockPromptService{}
	azureContext := NewAzureContext(mockPromptService, AzureScope{
		SubscriptionId: "test-subscription-id",
	}, nil)

	mockPromptService.On("PromptLocation", mockContextType, mockAzureContextType, mockSelectOptionsType).
		Return(&account.Location{
			Name: "test-location",
		}, nil)

	err := azureContext.EnsureLocation(context.Background())
	require.NoError(t, err)
	require.Equal(t, "test-location", azureContext.Scope.Location)

	mockPromptService.AssertCalled(t, "PromptLocation", mockContextType, mockAzureContextType, mockSelectOptionsType)
}

func TestAzureContext_EnsureLocation_NoPrompt(t *testing.T) {
	mockPromptService := &MockPromptService{}
	azureContext := NewAzureContext(mockPromptService, AzureScope{
		SubscriptionId: "test-subscription-id",
		Location:       "test-location",
	}, nil)

	err := azureContext.EnsureLocation(context.Background())
	require.NoError(t, err)
	require.Equal(t, "test-location", azureContext.Scope.Location)

	mockPromptService.AssertNotCalled(t, "PromptLocation", mock.Anything, mock.Anything, mock.Anything)
}

func TestAzureContext_EnsureLocation_Error(t *testing.T) {
	mockPromptService := &MockPromptService{}
	azureContext := NewAzureContext(mockPromptService, AzureScope{
		SubscriptionId: "test-subscription-id",
	}, nil)

	mockPromptService.On("PromptLocation", mockContextType, mockAzureContextType, mockSelectOptionsType).
		Return(nil, fmt.Errorf("location error"))

	err := azureContext.EnsureLocation(context.Background())
	require.Error(t, err)
	require.Equal(t, "", azureContext.Scope.Location)
}

type MockPromptService struct {
	mock.Mock
}

func (m *MockPromptService) PromptSubscription(
	ctx context.Context,
	selectorOptions *SelectOptions,
) (*account.Subscription, error) {
	args := m.Called(ctx, selectorOptions)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*account.Subscription), args.Error(1)
}

func (m *MockPromptService) PromptLocation(
	ctx context.Context,
	azureContext *AzureContext,
	selectorOptions *SelectOptions,
) (*account.Location, error) {
	args := m.Called(ctx, azureContext, selectorOptions)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*account.Location), args.Error(1)
}

func (m *MockPromptService) PromptResourceGroup(
	ctx context.Context,
	azureContext *AzureContext,
	options *ResourceGroupOptions,
) (*azapi.ResourceGroup, error) {
	args := m.Called(ctx, azureContext, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*azapi.ResourceGroup), args.Error(1)
}

func (m *MockPromptService) PromptSubscriptionResource(
	ctx context.Context,
	azureContext *AzureContext,
	options ResourceOptions,
) (*azapi.ResourceExtended, error) {
	args := m.Called(ctx, azureContext, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*azapi.ResourceExtended), args.Error(1)
}

func (m *MockPromptService) PromptResourceGroupResource(
	ctx context.Context,
	azureContext *AzureContext,
	options ResourceOptions,
) (*azapi.ResourceExtended, error) {
	args := m.Called(ctx, azureContext, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*azapi.ResourceExtended), args.Error(1)
}
