// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockprompt"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_PromptService_Confirm_NoPromptWithDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	resp, err := service.Confirm(context.Background(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "Continue?",
			DefaultValue: to.Ptr(true),
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Value)
	require.True(t, *resp.Value)
}

func Test_PromptService_Confirm_NoPromptWithoutDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	_, err := service.Confirm(context.Background(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message: "Continue?",
		},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "no default response")
}

func Test_PromptService_Select_NoPromptWithDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	resp, err := service.Select(context.Background(), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "Choose option:",
			SelectedIndex: to.Ptr(int32(1)),
			Choices: []*azdext.SelectChoice{
				{Value: "a", Label: "Option A"},
				{Value: "b", Label: "Option B"},
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Value)
	require.Equal(t, int32(1), *resp.Value)
}

func Test_PromptService_Select_NoPromptWithoutDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	_, err := service.Select(context.Background(), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Choose option:",
			Choices: []*azdext.SelectChoice{
				{Value: "a", Label: "Option A"},
			},
		},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "no default selection")
}

func Test_PromptService_MultiSelect_NoPrompt(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	resp, err := service.MultiSelect(context.Background(), &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: "Select items:",
			Choices: []*azdext.MultiSelectChoice{
				{Value: "a", Label: "Option A", Selected: true},
				{Value: "b", Label: "Option B", Selected: false},
				{Value: "c", Label: "Option C", Selected: true},
			},
		},
	})

	require.NoError(t, err)
	require.Len(t, resp.Values, 2)
	require.Equal(t, "a", resp.Values[0].Value)
	require.Equal(t, "c", resp.Values[1].Value)
}

func Test_PromptService_Prompt_NoPromptWithDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	resp, err := service.Prompt(context.Background(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "Enter name:",
			DefaultValue: "default-name",
			Required:     true,
		},
	})

	require.NoError(t, err)
	require.Equal(t, "default-name", resp.Value)
}

func Test_PromptService_Prompt_NoPromptRequiredWithoutDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	_, err := service.Prompt(context.Background(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:  "Enter name:",
			Required: true,
		},
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "no default response")
}

func Test_PromptService_Prompt_NoPromptNotRequiredWithoutDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	resp, err := service.Prompt(context.Background(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:  "Enter name:",
			Required: false,
		},
	})

	require.NoError(t, err)
	require.Equal(t, "", resp.Value)
}

func Test_PromptService_PromptSubscription(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	expectedSub := &account.Subscription{
		Id:       "/subscriptions/sub-123",
		Name:     "Test Subscription",
		TenantId: "tenant-123",
	}

	mockPrompter.
		On("PromptSubscription", mock.Anything, mock.Anything).
		Return(expectedSub, nil)

	service := NewPromptService(mockPrompter, nil, nil, globalOptions)

	resp, err := service.PromptSubscription(context.Background(), &azdext.PromptSubscriptionRequest{
		Message:     "Select subscription:",
		HelpMessage: "Choose your subscription",
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Subscription)
	require.Equal(t, expectedSub.Id, resp.Subscription.Id)
	require.Equal(t, expectedSub.Name, resp.Subscription.Name)
	require.Equal(t, expectedSub.TenantId, resp.Subscription.TenantId)
	mockPrompter.AssertExpectations(t)
}

func Test_PromptService_PromptLocation(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	expectedLocation := &account.Location{
		Name:                "eastus",
		DisplayName:         "East US",
		RegionalDisplayName: "(US) East US",
	}

	mockPrompter.
		On("PromptLocation", mock.Anything, mock.Anything, mock.Anything).
		Return(expectedLocation, nil)

	service := NewPromptService(mockPrompter, nil, nil, globalOptions)

	resp, err := service.PromptLocation(context.Background(), &azdext.PromptLocationRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Location)
	require.Equal(t, expectedLocation.Name, resp.Location.Name)
	require.Equal(t, expectedLocation.DisplayName, resp.Location.DisplayName)
	require.Equal(t, expectedLocation.RegionalDisplayName, resp.Location.RegionalDisplayName)
	mockPrompter.AssertExpectations(t)
}

func Test_PromptService_PromptResourceGroup(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	expectedRg := &azapi.ResourceGroup{
		Id:       "/subscriptions/sub-123/resourceGroups/rg-test",
		Name:     "rg-test",
		Location: "eastus",
	}

	mockPrompter.
		On("PromptResourceGroup", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *prompt.ResourceGroupOptions) bool {
			return opts != nil &&
				opts.SelectorOptions != nil &&
				opts.SelectorOptions.AllowNewResource != nil &&
				*opts.SelectorOptions.AllowNewResource == false &&
				opts.SelectorOptions.Message == "Select resource group" &&
				opts.SelectorOptions.EnableFiltering != nil &&
				*opts.SelectorOptions.EnableFiltering == true
		})).
		Return(expectedRg, nil)

	service := NewPromptService(mockPrompter, nil, nil, globalOptions)

	resp, err := service.PromptResourceGroup(context.Background(), &azdext.PromptResourceGroupRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
			},
		},
		Options: &azdext.PromptResourceGroupOptions{
			SelectOptions: &azdext.PromptResourceSelectOptions{
				AllowNewResource: to.Ptr(false),
				Message:          "Select resource group",
				EnableFiltering:  to.Ptr(true),
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.ResourceGroup)
	require.Equal(t, expectedRg.Id, resp.ResourceGroup.Id)
	require.Equal(t, expectedRg.Name, resp.ResourceGroup.Name)
	require.Equal(t, expectedRg.Location, resp.ResourceGroup.Location)
	mockPrompter.AssertExpectations(t)
}

func Test_PromptService_PromptResourceGroup_NilOptions(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	expectedRg := &azapi.ResourceGroup{
		Id:       "/subscriptions/sub-123/resourceGroups/rg-test",
		Name:     "rg-test",
		Location: "eastus",
	}

	mockPrompter.
		On("PromptResourceGroup", mock.Anything, mock.Anything, (*prompt.ResourceGroupOptions)(nil)).
		Return(expectedRg, nil)

	service := NewPromptService(mockPrompter, nil, nil, globalOptions)

	resp, err := service.PromptResourceGroup(context.Background(), &azdext.PromptResourceGroupRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.ResourceGroup)
	mockPrompter.AssertExpectations(t)
}

func Test_PromptService_PromptSubscriptionResource(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	expectedResource := &azapi.ResourceExtended{
		Resource: azapi.Resource{
			Id:       "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Storage/storageAccounts/storage1",
			Name:     "storage1",
			Type:     "Microsoft.Storage/storageAccounts",
			Location: "eastus",
		},
		Kind: "StorageV2",
	}

	mockPrompter.
		On(
			"PromptSubscriptionResource",
			mock.Anything,
			mock.Anything,
			mock.MatchedBy(func(opts prompt.ResourceOptions) bool {
				return opts.ResourceType != nil &&
					*opts.ResourceType == azapi.AzureResourceType("Microsoft.Storage/storageAccounts") &&
					len(opts.Kinds) == 2 &&
					opts.Kinds[0] == "StorageV2" &&
					opts.SelectorOptions != nil &&
					opts.SelectorOptions.AllowNewResource != nil &&
					*opts.SelectorOptions.AllowNewResource == false &&
					opts.SelectorOptions.Hint == "Filter storage accounts"
			}),
		).
		Return(expectedResource, nil)

	service := NewPromptService(mockPrompter, nil, nil, globalOptions)

	resp, err := service.PromptSubscriptionResource(context.Background(), &azdext.PromptSubscriptionResourceRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
			},
		},
		Options: &azdext.PromptResourceOptions{
			ResourceType: "Microsoft.Storage/storageAccounts",
			Kinds:        []string{"StorageV2", "BlobStorage"},
			SelectOptions: &azdext.PromptResourceSelectOptions{
				AllowNewResource: to.Ptr(false),
				Hint:             "Filter storage accounts",
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Resource)
	require.Equal(t, expectedResource.Id, resp.Resource.Id)
	require.Equal(t, expectedResource.Name, resp.Resource.Name)
	require.Equal(t, expectedResource.Type, resp.Resource.Type)
	require.Equal(t, expectedResource.Location, resp.Resource.Location)
	require.Equal(t, expectedResource.Kind, resp.Resource.Kind)
	mockPrompter.AssertExpectations(t)
}

func Test_PromptService_PromptResourceGroupResource(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	expectedResource := &azapi.ResourceExtended{
		Resource: azapi.Resource{
			Id:       "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Web/sites/webapp1",
			Name:     "webapp1",
			Type:     "Microsoft.Web/sites",
			Location: "eastus",
		},
	}

	mockPrompter.
		On(
			"PromptResourceGroupResource",
			mock.Anything,
			mock.Anything,
			mock.MatchedBy(func(opts prompt.ResourceOptions) bool {
				return opts.ResourceType != nil &&
					*opts.ResourceType == azapi.AzureResourceType("Microsoft.Web/sites") &&
					opts.ResourceTypeDisplayName == "Web App" &&
					opts.SelectorOptions != nil &&
					opts.SelectorOptions.Message == "Select a web app" &&
					opts.SelectorOptions.EnableFiltering != nil &&
					*opts.SelectorOptions.EnableFiltering == true
			}),
		).
		Return(expectedResource, nil)

	service := NewPromptService(mockPrompter, nil, nil, globalOptions)

	resp, err := service.PromptResourceGroupResource(context.Background(), &azdext.PromptResourceGroupResourceRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
				ResourceGroup:  "rg-test",
			},
		},
		Options: &azdext.PromptResourceOptions{
			ResourceType:            "Microsoft.Web/sites",
			ResourceTypeDisplayName: "Web App",
			SelectOptions: &azdext.PromptResourceSelectOptions{
				Message:         "Select a web app",
				EnableFiltering: to.Ptr(true),
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Resource)
	require.Equal(t, expectedResource.Id, resp.Resource.Id)
	require.Equal(t, expectedResource.Name, resp.Resource.Name)
	require.Equal(t, expectedResource.Type, resp.Resource.Type)
	mockPrompter.AssertExpectations(t)
}

func Test_CreateResourceGroupOptions(t *testing.T) {
	tests := []struct {
		name     string
		input    *azdext.PromptResourceGroupOptions
		expected *prompt.ResourceGroupOptions
	}{
		{
			name:     "nil options",
			input:    nil,
			expected: nil,
		},
		{
			name: "nil select options",
			input: &azdext.PromptResourceGroupOptions{
				SelectOptions: nil,
			},
			expected: nil,
		},
		{
			name: "with all options",
			input: &azdext.PromptResourceGroupOptions{
				SelectOptions: &azdext.PromptResourceSelectOptions{
					ForceNewResource:   to.Ptr(true),
					AllowNewResource:   to.Ptr(false),
					NewResourceMessage: "Create new RG",
					Message:            "Select RG",
					HelpMessage:        "Help text",
					LoadingMessage:     "Loading...",
					DisplayNumbers:     to.Ptr(true),
					DisplayCount:       10,
					Hint:               "Hint text",
					EnableFiltering:    to.Ptr(true),
				},
			},
			expected: &prompt.ResourceGroupOptions{
				SelectorOptions: &prompt.SelectOptions{
					ForceNewResource:   to.Ptr(true),
					AllowNewResource:   to.Ptr(false),
					NewResourceMessage: "Create new RG",
					Message:            "Select RG",
					HelpMessage:        "Help text",
					LoadingMessage:     "Loading...",
					DisplayNumbers:     to.Ptr(true),
					DisplayCount:       10,
					Hint:               "Hint text",
					EnableFiltering:    to.Ptr(true),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := createResourceGroupOptions(tt.input)

			if tt.expected == nil {
				require.Nil(t, result)
				return
			}

			require.NotNil(t, result)
			require.NotNil(t, result.SelectorOptions)
			require.Equal(t, tt.expected.SelectorOptions.ForceNewResource, result.SelectorOptions.ForceNewResource)
			require.Equal(t, tt.expected.SelectorOptions.AllowNewResource, result.SelectorOptions.AllowNewResource)
			require.Equal(t, tt.expected.SelectorOptions.NewResourceMessage, result.SelectorOptions.NewResourceMessage)
			require.Equal(t, tt.expected.SelectorOptions.Message, result.SelectorOptions.Message)
			require.Equal(t, tt.expected.SelectorOptions.HelpMessage, result.SelectorOptions.HelpMessage)
			require.Equal(t, tt.expected.SelectorOptions.LoadingMessage, result.SelectorOptions.LoadingMessage)
			require.Equal(t, tt.expected.SelectorOptions.DisplayNumbers, result.SelectorOptions.DisplayNumbers)
			require.Equal(t, tt.expected.SelectorOptions.DisplayCount, result.SelectorOptions.DisplayCount)
			require.Equal(t, tt.expected.SelectorOptions.Hint, result.SelectorOptions.Hint)
			require.Equal(t, tt.expected.SelectorOptions.EnableFiltering, result.SelectorOptions.EnableFiltering)
		})
	}
}

func Test_CreateResourceOptions(t *testing.T) {
	tests := []struct {
		name  string
		input *azdext.PromptResourceOptions
	}{
		{
			name:  "nil options",
			input: nil,
		},
		{
			name: "with resource type",
			input: &azdext.PromptResourceOptions{
				ResourceType:            "Microsoft.Storage/storageAccounts",
				Kinds:                   []string{"StorageV2", "BlobStorage"},
				ResourceTypeDisplayName: "Storage Account",
			},
		},
		{
			name: "with select options",
			input: &azdext.PromptResourceOptions{
				ResourceType: "Microsoft.Web/sites",
				SelectOptions: &azdext.PromptResourceSelectOptions{
					AllowNewResource: to.Ptr(true),
					Message:          "Select web app",
					EnableFiltering:  to.Ptr(true),
					Hint:             "Filter by name",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := createResourceOptions(tt.input)

			if tt.input == nil {
				require.Equal(t, prompt.ResourceOptions{}, result)
				return
			}

			if tt.input.ResourceType != "" {
				require.NotNil(t, result.ResourceType)
				require.Equal(t, azapi.AzureResourceType(tt.input.ResourceType), *result.ResourceType)
			}

			require.Equal(t, tt.input.Kinds, result.Kinds)
			require.Equal(t, tt.input.ResourceTypeDisplayName, result.ResourceTypeDisplayName)

			if tt.input.SelectOptions != nil {
				require.NotNil(t, result.SelectorOptions)
				require.Equal(t, tt.input.SelectOptions.AllowNewResource, result.SelectorOptions.AllowNewResource)
				require.Equal(t, tt.input.SelectOptions.Message, result.SelectorOptions.Message)
				require.Equal(t, tt.input.SelectOptions.EnableFiltering, result.SelectorOptions.EnableFiltering)
				require.Equal(t, tt.input.SelectOptions.Hint, result.SelectorOptions.Hint)
			}
		})
	}
}

func Test_WrapErrorWithSuggestion(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantNil     bool
		wantContain string
	}{
		{
			name:    "nil error returns nil",
			err:     nil,
			wantNil: true,
		},
		{
			name:        "error without suggestion is returned as-is",
			err:         errors.New("some error"),
			wantContain: "some error",
		},
		{
			name: "error with suggestion includes suggestion text",
			err: &internal.ErrorWithSuggestion{
				Err:        errors.New("authentication failed"),
				Suggestion: "Suggestion: run `azd auth login` to acquire a new token.",
			},
			wantContain: "azd auth login",
		},
		{
			name: "wrapped error with suggestion includes suggestion text",
			err: fmt.Errorf("failed to prompt: %w", &internal.ErrorWithSuggestion{
				Err:        errors.New("token expired"),
				Suggestion: "Suggestion: login expired, run `azd auth login` to acquire a new token.",
			}),
			wantContain: "azd auth login",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := wrapErrorWithSuggestion(tt.err)
			if tt.wantNil {
				require.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			require.Contains(t, result.Error(), tt.wantContain)
		})
	}
}

func Test_PromptService_PromptSubscription_ErrorWithSuggestion(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	authErr := &internal.ErrorWithSuggestion{
		Err:        errors.New("AADSTS70043: The refresh token has expired"),
		Suggestion: "Suggestion: login expired, run `azd auth login` to acquire a new token.",
	}

	mockPrompter.
		On("PromptSubscription", mock.Anything, mock.Anything).
		Return(nil, authErr)

	service := NewPromptService(mockPrompter, nil, nil, globalOptions)

	_, err := service.PromptSubscription(context.Background(), &azdext.PromptSubscriptionRequest{
		Message: "Select subscription:",
	})

	require.Error(t, err)
	// Verify that the suggestion text is included in the error message
	require.Contains(t, err.Error(), "azd auth login")
	require.Contains(t, err.Error(), "AADSTS70043")
	mockPrompter.AssertExpectations(t)
}

func Test_PromptService_PromptResourceGroup_ErrorWithSuggestion(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	authErr := &internal.ErrorWithSuggestion{
		Err:        errors.New("AADSTS70043: The refresh token has expired"),
		Suggestion: "Suggestion: login expired, run `azd auth login` to acquire a new token.",
	}

	mockPrompter.
		On("PromptResourceGroup", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, authErr)

	service := NewPromptService(mockPrompter, nil, nil, globalOptions)

	_, err := service.PromptResourceGroup(context.Background(), &azdext.PromptResourceGroupRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
			},
		},
	})

	require.Error(t, err)
	// Verify that the suggestion text is included in the error message
	require.Contains(t, err.Error(), "azd auth login")
	require.Contains(t, err.Error(), "AADSTS70043")
	mockPrompter.AssertExpectations(t)
}

func Test_validateDeploymentCapacity(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		sku         ai.AiModelSku
		want        int32
		errContains string
	}{
		{
			name:  "valid capacity with constraints",
			value: "20",
			sku: ai.AiModelSku{
				MinCapacity:  10,
				MaxCapacity:  100,
				CapacityStep: 10,
			},
			want: 20,
		},
		{
			name:        "non-numeric value",
			value:       "abc",
			sku:         ai.AiModelSku{},
			errContains: "whole number",
		},
		{
			name:  "below minimum",
			value: "5",
			sku: ai.AiModelSku{
				MinCapacity: 10,
			},
			errContains: "at least 10",
		},
		{
			name:  "above maximum",
			value: "120",
			sku: ai.AiModelSku{
				MaxCapacity: 100,
			},
			errContains: "at most 100",
		},
		{
			name:  "step mismatch",
			value: "25",
			sku: ai.AiModelSku{
				CapacityStep: 10,
			},
			errContains: "multiple of 10",
		},
		{
			name:  "trimmed input is accepted",
			value: " 30 ",
			sku: ai.AiModelSku{
				MinCapacity: 10,
			},
			want: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateDeploymentCapacity(tt.value, tt.sku)
			if tt.errContains != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_validateCapacityAgainstRemainingQuota(t *testing.T) {
	tests := []struct {
		name        string
		capacity    int32
		remaining   *float64
		errContains string
	}{
		{
			name:      "no remaining quota info",
			capacity:  100,
			remaining: nil,
		},
		{
			name:      "capacity within remaining quota",
			capacity:  10,
			remaining: to.Ptr(float64(25)),
		},
		{
			name:        "capacity exceeds remaining quota",
			capacity:    30,
			remaining:   to.Ptr(float64(20)),
			errContains: "at most 20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCapacityAgainstRemainingQuota(tt.capacity, tt.remaining)
			if tt.errContains != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
				return
			}

			require.NoError(t, err)
		})
	}
}

func Test_buildSkuCandidatesForVersion(t *testing.T) {
	version := ai.AiModelVersion{
		Version: "2024-06-01",
		Skus: []ai.AiModelSku{
			{
				Name:            "Standard",
				UsageName:       "OpenAI.Standard.gpt-4o",
				DefaultCapacity: 5,
				MinCapacity:     1,
				MaxCapacity:     100,
				CapacityStep:    1,
			},
			{
				Name:            "Standard",
				UsageName:       "OpenAI.Standard.gpt-4o-finetune",
				DefaultCapacity: 5,
				MinCapacity:     1,
				MaxCapacity:     100,
				CapacityStep:    1,
			},
		},
	}

	t.Run("excludes finetune skus when include flag is false", func(t *testing.T) {
		candidates := buildSkuCandidatesForVersion(version, nil, nil, nil, false)
		require.Len(t, candidates, 1)
		require.Equal(t, "OpenAI.Standard.gpt-4o", candidates[0].sku.UsageName)
	})

	t.Run("includes finetune skus when include flag is true", func(t *testing.T) {
		candidates := buildSkuCandidatesForVersion(version, nil, nil, nil, true)
		require.Len(t, candidates, 2)
	})

	t.Run("applies quota and capacity filters", func(t *testing.T) {
		options := &ai.DeploymentOptions{
			Capacity: to.Ptr(int32(5)),
		}
		quota := &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		}
		usageMap := map[string]ai.AiModelUsage{
			"OpenAI.Standard.gpt-4o": {
				Name:         "OpenAI.Standard.gpt-4o",
				CurrentValue: 6,
				Limit:        10, // remaining 4 < capacity 5 => excluded
			},
			"OpenAI.Standard.gpt-4o-finetune": {
				Name:         "OpenAI.Standard.gpt-4o-finetune",
				CurrentValue: 0,
				Limit:        10, // remaining 10 => included
			},
		}

		candidates := buildSkuCandidatesForVersion(version, options, quota, usageMap, true)
		require.Len(t, candidates, 1)
		require.Equal(t, "OpenAI.Standard.gpt-4o-finetune", candidates[0].sku.UsageName)
		require.NotNil(t, candidates[0].remaining)
		require.Equal(t, float64(10), *candidates[0].remaining)
	})
}

func Test_maxSkuCandidateRemaining(t *testing.T) {
	remainingA := float64(4)
	remainingB := float64(10)
	skuCandidates := []skuCandidate{
		{remaining: &remainingA},
		{remaining: nil},
		{remaining: &remainingB},
	}

	maxRemaining, found := maxSkuCandidateRemaining(skuCandidates)
	require.True(t, found)
	require.Equal(t, float64(10), maxRemaining)

	_, found = maxSkuCandidateRemaining([]skuCandidate{{remaining: nil}})
	require.False(t, found)
}
