// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockprompt"
)

func Test_PromptService_Confirm_NoPromptWithDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	resp, err := service.Confirm(t.Context(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "Continue?",
			DefaultValue: new(true),
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Value)
	require.True(t, *resp.Value)
}

func Test_PromptService_Confirm_NoPromptWithoutDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	_, err := service.Confirm(t.Context(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message: "Continue?",
		},
	})

	require.Error(t, err)
	requirePromptRequiredError(t, err, "Continue?")
}

func Test_PromptService_Select_NoPromptWithDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	resp, err := service.Select(t.Context(), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:       "Choose option:",
			SelectedIndex: new(int32(1)),
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

	_, err := service.Select(t.Context(), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Choose option:",
			Choices: []*azdext.SelectChoice{
				{Value: "a", Label: "Option A"},
			},
		},
	})

	require.Error(t, err)
	requirePromptRequiredError(t, err, "Choose option:")
}

func Test_PromptService_MultiSelect_NoPrompt(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	resp, err := service.MultiSelect(t.Context(), &azdext.MultiSelectRequest{
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

	resp, err := service.Prompt(t.Context(), &azdext.PromptRequest{
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

	_, err := service.Prompt(t.Context(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:  "Enter name:",
			Required: true,
		},
	})

	require.Error(t, err)
	requirePromptRequiredError(t, err, "Enter name:")
}

func Test_PromptService_Prompt_NoPromptNotRequiredWithoutDefault(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	resp, err := service.Prompt(t.Context(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:  "Enter name:",
			Required: false,
		},
	})

	require.NoError(t, err)
	require.Equal(t, "", resp.Value)
}

func requirePromptRequiredError(t *testing.T, err error, expectedPromptMessage string) *input.PromptRequiredError {
	t.Helper()

	promptErr, ok := errors.AsType[*input.PromptRequiredError](err)
	require.True(t, ok)
	require.Empty(t, promptErr.Inputs)
	require.Equal(t, expectedPromptMessage, promptErr.PromptMessage)

	return promptErr
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

	resp, err := service.PromptSubscription(t.Context(), &azdext.PromptSubscriptionRequest{
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

	resp, err := service.PromptLocation(t.Context(), &azdext.PromptLocationRequest{
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

func Test_PromptService_PromptLocation_WithAllowedLocations(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	expectedLocation := &account.Location{
		Name:                "westus3",
		DisplayName:         "West US 3",
		RegionalDisplayName: "(US) West US 3",
	}

	mockPrompter.
		On("PromptLocation", mock.Anything, mock.Anything, mock.MatchedBy(func(opts *prompt.SelectOptions) bool {
			return opts != nil && slices.Equal(opts.AllowedValues, []string{"westus3", "eastus2"})
		})).
		Return(expectedLocation, nil)

	service := NewPromptService(mockPrompter, nil, nil, globalOptions)

	resp, err := service.PromptLocation(t.Context(), &azdext.PromptLocationRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
			},
		},
		AllowedLocations: []string{"westus3", "eastus2"},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Location)
	require.Equal(t, expectedLocation.Name, resp.Location.Name)
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

	resp, err := service.PromptResourceGroup(t.Context(), &azdext.PromptResourceGroupRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
			},
		},
		Options: &azdext.PromptResourceGroupOptions{
			SelectOptions: &azdext.PromptResourceSelectOptions{
				AllowNewResource: new(false),
				Message:          "Select resource group",
				EnableFiltering:  new(true),
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

	resp, err := service.PromptResourceGroup(t.Context(), &azdext.PromptResourceGroupRequest{
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

	resp, err := service.PromptSubscriptionResource(t.Context(), &azdext.PromptSubscriptionResourceRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
			},
		},
		Options: &azdext.PromptResourceOptions{
			ResourceType: "Microsoft.Storage/storageAccounts",
			Kinds:        []string{"StorageV2", "BlobStorage"},
			SelectOptions: &azdext.PromptResourceSelectOptions{
				AllowNewResource: new(false),
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

	resp, err := service.PromptResourceGroupResource(t.Context(), &azdext.PromptResourceGroupResourceRequest{
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
				EnableFiltering: new(true),
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
					ForceNewResource:   new(true),
					AllowNewResource:   new(false),
					NewResourceMessage: "Create new RG",
					Message:            "Select RG",
					HelpMessage:        "Help text",
					LoadingMessage:     "Loading...",
					DisplayNumbers:     new(true),
					DisplayCount:       10,
					Hint:               "Hint text",
					EnableFiltering:    new(true),
				},
			},
			expected: &prompt.ResourceGroupOptions{
				SelectorOptions: &prompt.SelectOptions{
					ForceNewResource:   new(true),
					AllowNewResource:   new(false),
					NewResourceMessage: "Create new RG",
					Message:            "Select RG",
					HelpMessage:        "Help text",
					LoadingMessage:     "Loading...",
					DisplayNumbers:     new(true),
					DisplayCount:       10,
					Hint:               "Hint text",
					EnableFiltering:    new(true),
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
					AllowNewResource: new(true),
					Message:          "Select web app",
					EnableFiltering:  new(true),
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

// setupTestServer creates and starts a test gRPC server with the given prompt service,
// returning the server, authenticated context, client, and cleanup function
func setupTestServer(t *testing.T, promptSvc azdext.PromptServiceServer) (
	*Server, context.Context, *azdext.AzdClient, func(),
) {
	server := NewServer(
		azdext.UnimplementedProjectServiceServer{},
		azdext.UnimplementedEnvironmentServiceServer{},
		promptSvc,
		azdext.UnimplementedUserConfigServiceServer{},
		azdext.UnimplementedDeploymentServiceServer{},
		azdext.UnimplementedEventServiceServer{},
		azdext.UnimplementedComposeServiceServer{},
		azdext.UnimplementedWorkflowServiceServer{},
		azdext.UnimplementedExtensionServiceServer{},
		azdext.UnimplementedServiceTargetServiceServer{},
		azdext.UnimplementedFrameworkServiceServer{},
		azdext.UnimplementedContainerServiceServer{},
		azdext.UnimplementedAccountServiceServer{},
		azdext.UnimplementedAiModelServiceServer{},
		azdext.UnimplementedCopilotServiceServer{},
		azdext.UnimplementedProvisioningServiceServer{},
		azdext.UnimplementedValidationServiceServer{},
		azdext.UnimplementedTelemetryServiceServer{},
	)

	serverInfo, err := server.Start()
	require.NoError(t, err)

	extension := &extensions.Extension{
		Id: "azd.internal.test",
		Capabilities: []extensions.CapabilityType{
			extensions.CustomCommandCapability,
		},
		Namespace: "test",
	}

	accessToken, err := GenerateExtensionToken(extension, serverInfo)
	require.NoError(t, err)

	ctx := azdext.WithAccessToken(t.Context(), accessToken)
	client, err := azdext.NewAzdClient(azdext.WithAddress(serverInfo.Address))
	require.NoError(t, err)

	cleanup := func() {
		client.Close()
		err := server.Stop()
		require.NoError(t, err)
	}

	return server, ctx, client, cleanup
}

func Test_PromptService_PromptSubscription_ErrorWithSuggestion(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	authErr := &internal.ErrorWithSuggestion{
		Err:        errors.New("AADSTS70043: The refresh token has expired"),
		Suggestion: "login expired, run `azd auth login` to acquire a new token.",
	}

	mockPrompter.
		On("PromptSubscription", mock.Anything, mock.Anything).
		Return(nil, authErr)

	promptSvc := NewPromptService(mockPrompter, nil, nil, globalOptions)
	_, ctx, client, cleanup := setupTestServer(t, promptSvc)
	defer cleanup()

	_, err := client.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{
		Message: "Select subscription:",
	})

	require.Error(t, err)
	// Status message carries only the underlying error; suggestion travels via ActionableErrorDetail.
	require.Contains(t, err.Error(), "AADSTS70043")
	require.NotContains(t, err.Error(), "azd auth login",
		"suggestion text must not be concatenated into status.Message")
	st, ok := status.FromError(err)
	require.True(t, ok)
	actionable := azdext.ActionableErrorDetailFromStatus(st)
	require.NotNil(t, actionable)
	require.Contains(t, actionable.GetSuggestion(), "azd auth login")
	mockPrompter.AssertExpectations(t)
}

func Test_PromptService_PromptResourceGroup_ErrorWithSuggestion(t *testing.T) {
	mockPrompter := &mockprompt.MockPromptService{}
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}

	authErr := &internal.ErrorWithSuggestion{
		Err:        errors.New("AADSTS70043: The refresh token has expired"),
		Suggestion: "login expired, run `azd auth login` to acquire a new token.",
	}

	mockPrompter.
		On("PromptResourceGroup", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, authErr)

	promptSvc := NewPromptService(mockPrompter, nil, nil, globalOptions)
	_, ctx, client, cleanup := setupTestServer(t, promptSvc)
	defer cleanup()

	_, err := client.Prompt().PromptResourceGroup(ctx, &azdext.PromptResourceGroupRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: "sub-123",
			},
		},
	})

	require.Error(t, err)
	// Status message carries only the underlying error; suggestion travels via ActionableErrorDetail.
	require.Contains(t, err.Error(), "AADSTS70043")
	require.NotContains(t, err.Error(), "azd auth login",
		"suggestion text must not be concatenated into status.Message")
	st, ok := status.FromError(err)
	require.True(t, ok)
	actionable := azdext.ActionableErrorDetailFromStatus(st)
	require.NotNil(t, actionable)
	require.Contains(t, actionable.GetSuggestion(), "azd auth login")
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
			remaining: new(float64(25)),
		},
		{
			name:        "capacity exceeds remaining quota",
			capacity:    30,
			remaining:   new(float64(20)),
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
			Capacity: new(int32(5)),
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

	t.Run("falls back to lower capacity that fits remaining quota", func(t *testing.T) {
		deepSeekVersion := ai.AiModelVersion{
			Version: "1",
			Skus: []ai.AiModelSku{
				{
					Name:            "GlobalStandard",
					UsageName:       "AIServices.GlobalStandard.DeepSeek-R1-0528",
					DefaultCapacity: 5000,
					MinCapacity:     0,
					MaxCapacity:     5000,
					CapacityStep:    0,
				},
			},
		}
		quota := &azdext.QuotaCheckOptions{
			MinRemainingCapacity: 1,
		}
		usageMap := map[string]ai.AiModelUsage{
			"AIServices.GlobalStandard.DeepSeek-R1-0528": {
				Name:         "AIServices.GlobalStandard.DeepSeek-R1-0528",
				CurrentValue: 0,
				Limit:        1000,
			},
		}

		candidates := buildSkuCandidatesForVersion(deepSeekVersion, nil, quota, usageMap, false)
		require.Len(t, candidates, 1)
		require.Equal(t, "AIServices.GlobalStandard.DeepSeek-R1-0528", candidates[0].sku.UsageName)
		require.NotNil(t, candidates[0].remaining)
		require.Equal(t, float64(1000), *candidates[0].remaining)
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

func Test_findDefaultIndex(t *testing.T) {
	choices := []*ux.SelectChoice{
		{Value: "gpt-4o", Label: "gpt-4o"},
		{Value: "gpt-4o-mini", Label: "gpt-4o-mini"},
		{Value: "gpt-35-turbo", Label: "gpt-35-turbo"},
	}

	tests := []struct {
		name         string
		defaultValue string
		wantIndex    *int
	}{
		{
			name:         "exact match returns index",
			defaultValue: "gpt-4o-mini",
			wantIndex:    new(1),
		},
		{
			name:         "case insensitive match",
			defaultValue: "GPT-4O-MINI",
			wantIndex:    new(1),
		},
		{
			name:         "first item match",
			defaultValue: "gpt-4o",
			wantIndex:    new(0),
		},
		{
			name:         "last item match",
			defaultValue: "gpt-35-turbo",
			wantIndex:    new(2),
		},
		{
			name:         "no match returns nil",
			defaultValue: "nonexistent-model",
			wantIndex:    nil,
		},
		{
			name:         "empty default returns nil",
			defaultValue: "",
			wantIndex:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findDefaultIndex(choices, tt.defaultValue)
			if tt.wantIndex == nil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				require.Equal(t, *tt.wantIndex, *result)
			}
		})
	}
}

func Test_findDefaultIndex_EmptyChoices(t *testing.T) {
	result := findDefaultIndex([]*ux.SelectChoice{}, "some-value")
	require.Nil(t, result)
}

func Test_selectModelNoPrompt(t *testing.T) {
	models := []ai.AiModel{
		{
			Name:   "gpt-4o",
			Format: "OpenAI",
			Versions: []ai.AiModelVersion{
				{Version: "2024-05-13", IsDefault: true},
			},
			Locations: []string{"eastus"},
		},
		{
			Name:   "gpt-4o-mini",
			Format: "OpenAI",
			Versions: []ai.AiModelVersion{
				{Version: "2024-07-18", IsDefault: true},
			},
			Locations: []string{"eastus", "westus"},
		},
		{
			Name:      "gpt-35-turbo",
			Format:    "OpenAI",
			Locations: []string{"eastus"},
		},
	}

	tests := []struct {
		name         string
		models       []ai.AiModel
		defaultValue string
		wantModel    string
		errContains  string
	}{
		{
			name:         "exact match returns model",
			models:       models,
			defaultValue: "gpt-4o",
			wantModel:    "gpt-4o",
		},
		{
			name:         "case insensitive match",
			models:       models,
			defaultValue: "GPT-4O-MINI",
			wantModel:    "gpt-4o-mini",
		},
		{
			name:         "mixed case match",
			models:       models,
			defaultValue: "Gpt-35-Turbo",
			wantModel:    "gpt-35-turbo",
		},
		{
			name:         "no match returns not found error",
			models:       models,
			defaultValue: "nonexistent-model",
			errContains:  "not found in available models",
		},
		{
			name:         "empty default returns interactive required error",
			models:       models,
			defaultValue: "",
			errContains:  "cannot prompt for model selection in non-interactive mode",
		},
		{
			name:         "empty models with default returns not found",
			models:       []ai.AiModel{},
			defaultValue: "gpt-4o",
			errContains:  "not found in available models",
		},
		{
			name:         "empty models without default returns interactive required",
			models:       []ai.AiModel{},
			defaultValue: "",
			errContains:  "cannot prompt for model selection in non-interactive mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := selectModelNoPrompt(tt.models, tt.defaultValue)
			if tt.errContains != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
				require.Nil(t, resp)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)
			require.NotNil(t, resp.Model)
			require.Equal(t, tt.wantModel, resp.Model.Name)
		})
	}
}

func Test_PromptService_NilOptions_Validation(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: true}
	service := NewPromptService(nil, nil, nil, globalOptions)

	tests := []struct {
		name   string
		method string
	}{
		{"Confirm_nil_options", "Confirm"},
		{"Select_nil_options", "Select"},
		{"MultiSelect_nil_options", "MultiSelect"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			switch tt.method {
			case "Confirm":
				_, err = service.Confirm(
					t.Context(),
					&azdext.ConfirmRequest{Options: nil},
				)
			case "Select":
				_, err = service.Select(
					t.Context(),
					&azdext.SelectRequest{Options: nil},
				)
			case "MultiSelect":
				_, err = service.MultiSelect(
					t.Context(),
					&azdext.MultiSelectRequest{Options: nil},
				)
			}

			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok)
			require.Equal(t, codes.InvalidArgument, st.Code())
			require.Contains(t, st.Message(), "options are required")
		})
	}
}

func Test_PromptService_CreateAzureContext_NilScope(t *testing.T) {
	globalOptions := &internal.GlobalCommandOptions{NoPrompt: false}
	svc := NewPromptService(nil, nil, nil, globalOptions)
	ps := svc.(*promptService)

	tests := []struct {
		name        string
		wire        *azdext.AzureContext
		errContains string
	}{
		{
			name:        "nil_azure_context",
			wire:        nil,
			errContains: "azure context is required",
		},
		{
			name:        "nil_scope",
			wire:        &azdext.AzureContext{Scope: nil},
			errContains: "azure context scope is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ps.createAzureContext(tt.wire)
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok)
			require.Equal(t, codes.InvalidArgument, st.Code())
			require.Contains(t, st.Message(), tt.errContains)
		})
	}
}

// --- convertToInt32 tests (table-driven) ---

func TestConvertToInt32(t *testing.T) {
	t.Parallel()
	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, convertToInt32(nil))
	})
	for _, tc := range []struct {
		name     string
		input    int
		expected int32
	}{
		{"positive", 42, 42},
		{"zero", 0, 0},
		{"negative", -7, -7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := convertToInt32(&tc.input)
			require.NotNil(t, result)
			require.Equal(t, tc.expected, *result)
		})
	}
}

// --- convertToInt tests (table-driven) ---

func TestConvertToInt(t *testing.T) {
	t.Parallel()
	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, convertToInt(nil))
	})
	for _, tc := range []struct {
		name     string
		input    int32
		expected int
	}{
		{"positive", 99, 99},
		{"zero", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := convertToInt(&tc.input)
			require.NotNil(t, result)
			require.Equal(t, tc.expected, *result)
		})
	}
}

// --- requirePromptSubscriptionID tests (table-driven) ---

func TestRequirePromptSubscriptionID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		ctx       *azdext.AzureContext
		wantSubID string
		wantErr   bool
		wantCode  codes.Code
	}{
		{
			name:     "nil context",
			ctx:      nil,
			wantErr:  true,
			wantCode: codes.InvalidArgument,
		},
		{
			name:    "nil scope",
			ctx:     &azdext.AzureContext{},
			wantErr: true,
		},
		{
			name: "empty subscription ID",
			ctx: &azdext.AzureContext{
				Scope: &azdext.AzureScope{SubscriptionId: ""},
			},
			wantErr: true,
		},
		{
			name: "valid",
			ctx: &azdext.AzureContext{
				Scope: &azdext.AzureScope{SubscriptionId: "sub-123"},
			},
			wantSubID: "sub-123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			subID, err := requirePromptSubscriptionID(tt.ctx)
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantCode != 0 {
					st, ok := status.FromError(err)
					require.True(t, ok)
					require.Equal(t, tt.wantCode, st.Code())
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantSubID, subID)
			}
		})
	}
}

// --- requireSubscriptionID tests (ai_model_service helpers) ---

func TestRequireSubscriptionID_NilContext(t *testing.T) {
	t.Parallel()
	_, err := requireSubscriptionID(nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestRequireSubscriptionID_NilScope(t *testing.T) {
	t.Parallel()
	_, err := requireSubscriptionID(&azdext.AzureContext{})
	require.Error(t, err)
}

func TestRequireSubscriptionID_EmptySubscriptionID(t *testing.T) {
	t.Parallel()
	_, err := requireSubscriptionID(&azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: ""},
	})
	require.Error(t, err)
}

func TestRequireSubscriptionID_Valid(t *testing.T) {
	t.Parallel()
	subId, err := requireSubscriptionID(&azdext.AzureContext{
		Scope: &azdext.AzureScope{SubscriptionId: "sub-abc"},
	})
	require.NoError(t, err)
	require.Equal(t, "sub-abc", subId)
}

// --- protoToFilterOptions tests ---

func TestProtoToFilterOptions_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, protoToFilterOptions(nil))
}

func TestProtoToFilterOptions_WithValues(t *testing.T) {
	t.Parallel()
	opts := protoToFilterOptions(&azdext.AiModelFilterOptions{
		Locations:         []string{"eastus", "westus"},
		Capabilities:      []string{"chat"},
		Formats:           []string{"json"},
		Statuses:          []string{"active"},
		ExcludeModelNames: []string{"gpt-3"},
	})
	require.NotNil(t, opts)
	require.Equal(t, []string{"eastus", "westus"}, opts.Locations)
	require.Equal(t, []string{"chat"}, opts.Capabilities)
	require.Equal(t, []string{"json"}, opts.Formats)
	require.Equal(t, []string{"active"}, opts.Statuses)
	require.Equal(t, []string{"gpt-3"}, opts.ExcludeModelNames)
}

// --- protoToDeploymentOptions tests ---

func TestProtoToDeploymentOptions_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, protoToDeploymentOptions(nil))
}

func TestProtoToDeploymentOptions_WithValues(t *testing.T) {
	t.Parallel()
	cap := int32(100)
	opts := protoToDeploymentOptions(&azdext.AiModelDeploymentOptions{
		Locations: []string{"eastus"},
		Versions:  []string{"v1"},
		Skus:      []string{"S0"},
		Capacity:  &cap,
	})
	require.NotNil(t, opts)
	require.Equal(t, []string{"eastus"}, opts.Locations)
	require.Equal(t, []string{"v1"}, opts.Versions)
	require.Equal(t, []string{"S0"}, opts.Skus)
	require.NotNil(t, opts.Capacity)
	require.Equal(t, int32(100), *opts.Capacity)
}

func TestProtoToDeploymentOptions_NoCapacity(t *testing.T) {
	t.Parallel()
	opts := protoToDeploymentOptions(&azdext.AiModelDeploymentOptions{
		Locations: []string{"eastus"},
	})
	require.NotNil(t, opts)
	require.Nil(t, opts.Capacity)
}

// --- protoToQuotaCheckOptions tests ---

func TestProtoToQuotaCheckOptions_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, protoToQuotaCheckOptions(nil))
}

func TestProtoToQuotaCheckOptions_WithValues(t *testing.T) {
	t.Parallel()
	opts := protoToQuotaCheckOptions(&azdext.QuotaCheckOptions{
		MinRemainingCapacity: 50.0,
	})
	require.NotNil(t, opts)
	require.Equal(t, 50.0, opts.MinRemainingCapacity)
}

// --- buildAgentOptions tests ---

func TestBuildAgentOptions_Defaults(t *testing.T) {
	t.Parallel()
	opts := buildAgentOptions("", "", "", "", false, false)
	require.Len(t, opts, 1) // only WithHeadless(false)
}

func TestBuildAgentOptions_AllSet(t *testing.T) {
	t.Parallel()
	opts := buildAgentOptions("gpt-4o", "high", "You are helpful", "plan", true, true)
	// WithHeadless(true) + WithModel + WithReasoningEffort + WithSystemMessage + WithMode + WithDebug
	require.Len(t, opts, 6)
}

func TestBuildAgentOptions_Partial(t *testing.T) {
	t.Parallel()
	opts := buildAgentOptions("gpt-4o", "", "", "", false, true)
	// WithHeadless(true) + WithModel("gpt-4o")
	require.Len(t, opts, 2)
}

// --- convertFileChangeType tests ---

func TestConvertFileChangeType_Created(t *testing.T) {
	t.Parallel()
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_CREATED,
		convertFileChangeType(watch.FileCreated))
}

func TestConvertFileChangeType_Modified(t *testing.T) {
	t.Parallel()
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_MODIFIED,
		convertFileChangeType(watch.FileModified))
}

func TestConvertFileChangeType_Deleted(t *testing.T) {
	t.Parallel()
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_DELETED,
		convertFileChangeType(watch.FileDeleted))
}

func TestConvertFileChangeType_Unknown(t *testing.T) {
	t.Parallel()
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_UNSPECIFIED,
		convertFileChangeType(watch.FileChangeType(999)))
}

// --- convertFileChanges tests ---

func TestConvertFileChanges_Empty(t *testing.T) {
	t.Parallel()
	result := convertFileChanges(nil)
	require.Nil(t, result)

	result = convertFileChanges([]watch.FileChange{})
	require.Nil(t, result)
}

func TestConvertFileChanges_WithChanges(t *testing.T) {
	t.Parallel()
	changes := []watch.FileChange{
		{Path: "/tmp/test.go", ChangeType: watch.FileCreated},
		{Path: "/tmp/test2.go", ChangeType: watch.FileModified},
	}
	result := convertFileChanges(changes)
	require.Len(t, result, 2)
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_CREATED, result[0].ChangeType)
	assert.Equal(t, azdext.CopilotFileChangeType_COPILOT_FILE_CHANGE_TYPE_MODIFIED, result[1].ChangeType)
}

// --- convertUsageMetrics tests ---

func TestConvertUsageMetrics(t *testing.T) {
	t.Parallel()
	usage := agent.UsageMetrics{
		Model:           "gpt-4o",
		InputTokens:     100,
		OutputTokens:    50,
		BillingRate:     0.5,
		PremiumRequests: 2,
		DurationMS:      1500,
	}
	result := convertUsageMetrics(usage)
	require.Equal(t, "gpt-4o", result.Model)
	require.Equal(t, float64(100), result.InputTokens)
	require.Equal(t, float64(50), result.OutputTokens)
	require.Equal(t, float64(150), result.TotalTokens) // 100 + 50
	require.Equal(t, 0.5, result.BillingRate)
	require.Equal(t, float64(2), result.PremiumRequests)
	require.Equal(t, float64(1500), result.DurationMs)
}

// --- convertSessionEvent tests ---

func TestConvertSessionEvent_BasicFields(t *testing.T) {
	t.Parallel()
	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	event := agent.SessionEvent{
		Type:      copilot.SessionEventTypeAssistantMessage,
		Timestamp: ts,
		Data:      &copilot.AssistantMessageData{Content: "hello"},
	}
	result := convertSessionEvent(event)
	require.Equal(t, "assistant.message", result.Type)
	require.Equal(t, "2024-01-15T10:30:00.000Z", result.Timestamp)
}

func TestConvertSessionEvent_WithToolStart(t *testing.T) {
	t.Parallel()
	event := agent.SessionEvent{
		Type:      copilot.SessionEventTypeToolExecutionStart,
		Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Data: &copilot.ToolExecutionStartData{
			ToolName:   "read_file",
			ToolCallID: "tc-123",
		},
	}
	result := convertSessionEvent(event)
	require.Equal(t, "tool.execution_start", result.Type)
	require.NotNil(t, result.Data)
	require.Equal(t, "read_file", result.Data.Fields["toolName"].GetStringValue())
}

func TestConvertSessionEvent_WithUsageData(t *testing.T) {
	t.Parallel()
	inputTokens := float64(500)
	event := agent.SessionEvent{
		Type:      copilot.SessionEventTypeAssistantUsage,
		Timestamp: time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
		Data: &copilot.AssistantUsageData{
			Model:       "gpt-4o",
			InputTokens: &inputTokens,
		},
	}
	result := convertSessionEvent(event)
	require.Equal(t, "assistant.usage", result.Type)
	require.NotNil(t, result.Data)
}

// --- modelQuotaSummary tests ---

func TestModelQuotaSummary_NoVersions(t *testing.T) {
	t.Parallel()
	model := ai.AiModel{Name: "gpt-4o"}
	result := modelQuotaSummary(model, nil)
	require.Equal(t, output.WithGrayFormat("[no quota info]"), result)
}

func TestModelQuotaSummary_NoMatchingUsage(t *testing.T) {
	t.Parallel()
	model := ai.AiModel{
		Name: "gpt-4o",
		Versions: []ai.AiModelVersion{
			{Skus: []ai.AiModelSku{{UsageName: "sku-1"}}},
		},
	}
	usageMap := map[string]ai.AiModelUsage{}
	result := modelQuotaSummary(model, usageMap)
	require.Equal(t, output.WithGrayFormat("[no quota info]"), result)
}

func TestModelQuotaSummary_WithQuota(t *testing.T) {
	t.Parallel()
	model := ai.AiModel{
		Name: "gpt-4o",
		Versions: []ai.AiModelVersion{
			{Skus: []ai.AiModelSku{
				{UsageName: "sku-1"},
				{UsageName: "sku-2"},
			}},
		},
	}
	usageMap := map[string]ai.AiModelUsage{
		"sku-1": {Limit: 1000, CurrentValue: 200},
		"sku-2": {Limit: 500, CurrentValue: 100},
	}
	result := modelQuotaSummary(model, usageMap)
	require.Equal(t, output.WithGrayFormat("[up to %.0f quota available]", float64(800)), result)
}

// --- selectModelNoPrompt tests ---

func TestSelectModelNoPrompt_EmptyDefault(t *testing.T) {
	t.Parallel()
	models := []ai.AiModel{{Name: "gpt-4o"}}
	_, err := selectModelNoPrompt(models, "")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestSelectModelNoPrompt_MatchFound(t *testing.T) {
	t.Parallel()
	models := []ai.AiModel{
		{Name: "gpt-3.5"},
		{Name: "gpt-4o"},
	}
	resp, err := selectModelNoPrompt(models, "GPT-4O") // case-insensitive
	require.NoError(t, err)
	require.NotNil(t, resp.Model)
}

func TestSelectModelNoPrompt_NoMatch(t *testing.T) {
	t.Parallel()
	models := []ai.AiModel{{Name: "gpt-4o"}}
	_, err := selectModelNoPrompt(models, "nonexistent")
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, st.Code())
}

// --- findDefaultIndex tests ---

func TestFindDefaultIndex_Empty(t *testing.T) {
	t.Parallel()
	result := findDefaultIndex(nil, "test")
	require.Nil(t, result)
}

func TestFindDefaultIndex_EmptyDefault(t *testing.T) {
	t.Parallel()
	choices := []*ux.SelectChoice{{Value: "a"}}
	result := findDefaultIndex(choices, "")
	require.Nil(t, result)
}

func TestFindDefaultIndex_Found(t *testing.T) {
	t.Parallel()
	choices := []*ux.SelectChoice{
		{Value: "alpha"},
		{Value: "beta"},
		{Value: "gamma"},
	}
	result := findDefaultIndex(choices, "BETA") // case-insensitive
	require.NotNil(t, result)
	require.Equal(t, 1, *result)
}

func TestFindDefaultIndex_NotFound(t *testing.T) {
	t.Parallel()
	choices := []*ux.SelectChoice{
		{Value: "alpha"},
		{Value: "beta"},
	}
	result := findDefaultIndex(choices, "delta")
	require.Nil(t, result)
}

// --- maxSkuCandidateRemaining tests ---

func TestMaxSkuCandidateRemaining_Empty(t *testing.T) {
	t.Parallel()
	_, found := maxSkuCandidateRemaining(nil)
	require.False(t, found)
}

func TestMaxSkuCandidateRemaining_AllNilRemaining(t *testing.T) {
	t.Parallel()
	candidates := []skuCandidate{
		{remaining: nil},
		{remaining: nil},
	}
	_, found := maxSkuCandidateRemaining(candidates)
	require.False(t, found)
}

func TestMaxSkuCandidateRemaining_WithValues(t *testing.T) {
	t.Parallel()
	r1 := float64(100)
	r2 := float64(500)
	r3 := float64(200)
	candidates := []skuCandidate{
		{remaining: &r1},
		{remaining: &r2},
		{remaining: &r3},
	}
	max, found := maxSkuCandidateRemaining(candidates)
	require.True(t, found)
	require.Equal(t, float64(500), max)
}

func TestMaxSkuCandidateRemaining_MixedNilAndValues(t *testing.T) {
	t.Parallel()
	r1 := float64(300)
	candidates := []skuCandidate{
		{remaining: nil},
		{remaining: &r1},
		{remaining: nil},
	}
	max, found := maxSkuCandidateRemaining(candidates)
	require.True(t, found)
	require.Equal(t, float64(300), max)
}

// --- buildSkuCandidatesForVersion tests ---

func TestBuildSkuCandidatesForVersion_EmptySkus(t *testing.T) {
	t.Parallel()
	version := ai.AiModelVersion{}
	result := buildSkuCandidatesForVersion(version, nil, nil, nil, false)
	require.Empty(t, result)
}

func TestBuildSkuCandidatesForVersion_NoQuotaCheck(t *testing.T) {
	t.Parallel()
	version := ai.AiModelVersion{
		Skus: []ai.AiModelSku{
			{Name: "S0", UsageName: "openai-standard"},
			{Name: "P1", UsageName: "openai-provisioned"},
		},
	}
	result := buildSkuCandidatesForVersion(version, nil, nil, nil, false)
	require.Len(t, result, 2)
}

func TestBuildSkuCandidatesForVersion_SkuFilter(t *testing.T) {
	t.Parallel()
	version := ai.AiModelVersion{
		Skus: []ai.AiModelSku{
			{Name: "S0", UsageName: "standard"},
			{Name: "P1", UsageName: "provisioned"},
		},
	}
	options := &ai.DeploymentOptions{Skus: []string{"S0"}}
	result := buildSkuCandidatesForVersion(version, options, nil, nil, false)
	require.Len(t, result, 1)
	require.Equal(t, "S0", result[0].sku.Name)
}

// --- validateDeploymentCapacity tests ---

func TestValidateDeploymentCapacity_Invalid(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{}
	_, err := validateDeploymentCapacity("abc", sku)
	require.Error(t, err)
	require.Contains(t, err.Error(), "whole number")
}

func TestValidateDeploymentCapacity_Zero(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{}
	_, err := validateDeploymentCapacity("0", sku)
	require.Error(t, err)
	require.Contains(t, err.Error(), "greater than 0")
}

func TestValidateDeploymentCapacity_BelowMin(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{MinCapacity: 10}
	_, err := validateDeploymentCapacity("5", sku)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 10")
}

func TestValidateDeploymentCapacity_AboveMax(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{MaxCapacity: 100}
	_, err := validateDeploymentCapacity("200", sku)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most 100")
}

func TestValidateDeploymentCapacity_WrongStep(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{CapacityStep: 10}
	_, err := validateDeploymentCapacity("15", sku)
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple of 10")
}

func TestValidateDeploymentCapacity_Valid(t *testing.T) {
	t.Parallel()
	sku := ai.AiModelSku{MinCapacity: 10, MaxCapacity: 100, CapacityStep: 10}
	cap, err := validateDeploymentCapacity("50", sku)
	require.NoError(t, err)
	require.Equal(t, int32(50), cap)
}

// --- validateCapacityAgainstRemainingQuota tests ---

func TestValidateCapacityAgainstRemainingQuota_NilRemaining(t *testing.T) {
	t.Parallel()
	err := validateCapacityAgainstRemainingQuota(100, nil)
	require.NoError(t, err)
}

func TestValidateCapacityAgainstRemainingQuota_Exceeds(t *testing.T) {
	t.Parallel()
	remaining := float64(50)
	err := validateCapacityAgainstRemainingQuota(100, &remaining)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at most 50")
}

func TestValidateCapacityAgainstRemainingQuota_WithinLimit(t *testing.T) {
	t.Parallel()
	remaining := float64(200)
	err := validateCapacityAgainstRemainingQuota(100, &remaining)
	require.NoError(t, err)
}

// --- createAzureContext tests ---

func TestCreateAzureContext_NilWire(t *testing.T) {
	t.Parallel()
	svc := &promptService{}
	_, err := svc.createAzureContext(nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateAzureContext_NilScope(t *testing.T) {
	t.Parallel()
	svc := &promptService{}
	_, err := svc.createAzureContext(&azdext.AzureContext{})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCreateAzureContext_InvalidResourceID(t *testing.T) {
	t.Parallel()
	svc := &promptService{}
	_, err := svc.createAzureContext(&azdext.AzureContext{
		Scope:     &azdext.AzureScope{SubscriptionId: "sub-1"},
		Resources: []string{"not-a-valid-resource-id"},
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())
}

// --- createResourceOptions tests ---

func TestCreateResourceOptions_Nil(t *testing.T) {
	t.Parallel()
	opts := createResourceOptions(nil)
	require.Nil(t, opts.ResourceType)
}

func TestCreateResourceOptions_WithValues(t *testing.T) {
	t.Parallel()
	opts := createResourceOptions(&azdext.PromptResourceOptions{
		ResourceType:            "Microsoft.Web/sites",
		Kinds:                   []string{"web"},
		ResourceTypeDisplayName: "Web App",
		SelectOptions: &azdext.PromptResourceSelectOptions{
			Message:     "Select a web app",
			HelpMessage: "Choose one",
		},
	})
	require.NotNil(t, opts.ResourceType)
	require.Equal(t, []string{"web"}, opts.Kinds)
	require.Equal(t, "Web App", opts.ResourceTypeDisplayName)
	require.NotNil(t, opts.SelectorOptions)
	require.Equal(t, "Select a web app", opts.SelectorOptions.Message)
}

// --- createResourceGroupOptions tests ---

func TestCreateResourceGroupOptions_Nil(t *testing.T) {
	t.Parallel()
	require.Nil(t, createResourceGroupOptions(nil))
}

func TestCreateResourceGroupOptions_NilSelectOptions(t *testing.T) {
	t.Parallel()
	require.Nil(t, createResourceGroupOptions(&azdext.PromptResourceGroupOptions{}))
}

func TestCreateResourceGroupOptions_WithValues(t *testing.T) {
	t.Parallel()
	allowNew := true
	result := createResourceGroupOptions(&azdext.PromptResourceGroupOptions{
		SelectOptions: &azdext.PromptResourceSelectOptions{
			Message:          "Select RG",
			AllowNewResource: &allowNew,
			DisplayCount:     10,
		},
	})
	require.NotNil(t, result)
	require.NotNil(t, result.SelectorOptions)
	require.Equal(t, "Select RG", result.SelectorOptions.Message)
	require.NotNil(t, result.SelectorOptions.AllowNewResource)
	require.True(t, *result.SelectorOptions.AllowNewResource)
	require.Equal(t, 10, result.SelectorOptions.DisplayCount)
}

// --- promptLock tests ---

func TestNewPromptLock(t *testing.T) {
	t.Parallel()
	lock := newPromptLock()
	require.NotNil(t, lock)
	require.NotNil(t, lock.ch)
}

func TestAcquirePromptLock_Success(t *testing.T) {
	t.Parallel()
	svc := &promptService{lock: newPromptLock()}
	release, err := svc.acquirePromptLock(t.Context())
	require.NoError(t, err)
	require.NotNil(t, release)

	// Release the lock
	release()
}

func TestAcquirePromptLock_CancelledContext(t *testing.T) {
	t.Parallel()
	svc := &promptService{lock: newPromptLock()}

	// Acquire the lock first
	release1, err := svc.acquirePromptLock(t.Context())
	require.NoError(t, err)

	// Try to acquire with a cancelled context
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	_, err = svc.acquirePromptLock(ctx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)

	release1()
}

// --- PromptAi* method tests (validation paths) ---

func TestPromptService_PromptAiModel_NilSubscription(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiModel(t.Context(), &azdext.PromptAiModelRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestPromptService_PromptAiDeployment_NilSubscription(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiDeployment(t.Context(), &azdext.PromptAiDeploymentRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestPromptService_PromptAiDeployment_QuotaRequiresOneLocation(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiDeployment(t.Context(), &azdext.PromptAiDeploymentRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub-123"},
		},
		ModelName: "gpt-4",
		Quota:     &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		Options:   nil, // no locations
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "quota checking requires exactly one effective location")
}

func TestPromptService_PromptAiDeployment_QuotaWithMultipleLocations(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiDeployment(t.Context(), &azdext.PromptAiDeploymentRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub-123"},
		},
		ModelName: "gpt-4",
		Quota:     &azdext.QuotaCheckOptions{MinRemainingCapacity: 1},
		Options:   &azdext.AiModelDeploymentOptions{Locations: []string{"eastus", "westus"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "quota checking requires exactly one effective location")
}

func TestPromptService_PromptAiLocationWithQuota_NilSubscription(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiLocationWithQuota(t.Context(), &azdext.PromptAiLocationWithQuotaRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestPromptService_PromptAiModelLocationWithQuota_NilSubscription(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiModelLocationWithQuota(t.Context(), &azdext.PromptAiModelLocationWithQuotaRequest{
		AzureContext: nil,
	})
	require.Error(t, err)
}

func TestPromptService_PromptAiModelLocationWithQuota_EmptyModelName(t *testing.T) {
	t.Parallel()
	svc := NewPromptService(nil, nil, nil, nil)
	_, err := svc.PromptAiModelLocationWithQuota(t.Context(), &azdext.PromptAiModelLocationWithQuotaRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: "sub-123"},
		},
		ModelName: "",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "model_name is required")
}

func (m *mockPromptService) PromptSubscription(
	ctx context.Context, opts *prompt.SelectOptions,
) (*account.Subscription, error) {
	if m.promptSubscriptionFn != nil {
		return m.promptSubscriptionFn(ctx, opts)
	}
	return nil, errors.New("not implemented")
}

func (m *mockPromptService) PromptLocation(
	ctx context.Context, ac *prompt.AzureContext, opts *prompt.SelectOptions,
) (*account.Location, error) {
	if m.promptLocationFn != nil {
		return m.promptLocationFn(ctx, ac, opts)
	}
	return nil, errors.New("not implemented")
}

func (m *mockPromptService) PromptResourceGroup(
	ctx context.Context, ac *prompt.AzureContext, opts *prompt.ResourceGroupOptions,
) (*azapi.ResourceGroup, error) {
	if m.promptResourceGroupFn != nil {
		return m.promptResourceGroupFn(ctx, ac, opts)
	}
	return nil, errors.New("not implemented")
}

func (m *mockPromptService) PromptSubscriptionResource(
	ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
) (*azapi.ResourceExtended, error) {
	if m.promptSubscriptionResourceFn != nil {
		return m.promptSubscriptionResourceFn(ctx, ac, opts)
	}
	return nil, errors.New("not implemented")
}

func (m *mockPromptService) PromptResourceGroupResource(
	ctx context.Context, ac *prompt.AzureContext, opts prompt.ResourceOptions,
) (*azapi.ResourceExtended, error) {
	if m.promptResourceGroupResourceFn != nil {
		return m.promptResourceGroupResourceFn(ctx, ac, opts)
	}
	return nil, errors.New("not implemented")
}

func newTestPromptService(prompter *mockPromptService, noPrompt bool) azdext.PromptServiceServer {
	return NewPromptService(prompter, nil, nil, &internal.GlobalCommandOptions{NoPrompt: noPrompt})
}

func TestPromptService_Confirm_NilRequest(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.Confirm(t.Context(), nil)
	require.Error(t, err)
}

func TestPromptService_Confirm_NilOptions(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.Confirm(t.Context(), &azdext.ConfirmRequest{})
	require.Error(t, err)
}

func TestPromptService_Confirm_NoPrompt_WithDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.Confirm(t.Context(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "continue?",
			DefaultValue: new(true),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Value)
	require.True(t, *resp.Value)
}

func TestPromptService_Confirm_NoPrompt_NoDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.Confirm(t.Context(), &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message: "continue?",
		},
	})
	require.Error(t, err)
	requirePromptRequiredError(t, err, "continue?")
}

func TestPromptService_MultiSelect_NilRequest(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.MultiSelect(t.Context(), nil)
	require.Error(t, err)
}

func TestPromptService_MultiSelect_NilOptions(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.MultiSelect(t.Context(), &azdext.MultiSelectRequest{})
	require.Error(t, err)
}

func TestPromptService_MultiSelect_NoPrompt_ReturnsSelected(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.MultiSelect(t.Context(), &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: "pick:",
			Choices: []*azdext.MultiSelectChoice{
				{Value: "a", Label: "A", Selected: true},
				{Value: "b", Label: "B", Selected: false},
				{Value: "c", Label: "C", Selected: true},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.Values, 2)
	require.Equal(t, "a", resp.Values[0].Value)
	require.Equal(t, "c", resp.Values[1].Value)
}

func TestPromptService_MultiSelect_NoPrompt_NoneSelected(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.MultiSelect(t.Context(), &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: "pick:",
			Choices: []*azdext.MultiSelectChoice{
				{Value: "a", Label: "A", Selected: false},
			},
		},
	})
	require.NoError(t, err)
	require.Empty(t, resp.Values)
}

func TestPromptService_Prompt_NoPrompt_WithDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.Prompt(t.Context(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "enter value:",
			DefaultValue: "mydefault",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "mydefault", resp.Value)
}

func TestPromptService_Prompt_NoPrompt_RequiredNoDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.Prompt(t.Context(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:  "enter:",
			Required: true,
		},
	})
	require.Error(t, err)
	requirePromptRequiredError(t, err, "enter:")
}

func TestPromptService_Prompt_NoPrompt_RequiredWithDefault(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	resp, err := svc.Prompt(t.Context(), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:      "enter:",
			Required:     true,
			DefaultValue: "provided",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "provided", resp.Value)
}

func TestPromptService_PromptSubscription_NoPrompt_DefaultMessage(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.PromptSubscription(t.Context(), &azdext.PromptSubscriptionRequest{})
	require.Error(t, err)
	requirePromptRequiredError(t, err, "Select subscription")
}

func TestPromptService_PromptLocation_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.PromptLocation(t.Context(), &azdext.PromptLocationRequest{})
	require.Error(t, err)
}

func TestPromptService_PromptLocation_NoPrompt(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.PromptLocation(t.Context(), &azdext.PromptLocationRequest{})
	require.Error(t, err)
	requirePromptRequiredError(t, err, "Select location")
}

func TestPromptService_PromptResourceGroup_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.PromptResourceGroup(t.Context(), &azdext.PromptResourceGroupRequest{})
	require.Error(t, err)
}

func TestPromptService_PromptResourceGroup_NoPrompt_DefaultMessage(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.PromptResourceGroup(t.Context(), &azdext.PromptResourceGroupRequest{})
	require.Error(t, err)
	requirePromptRequiredError(t, err, "Select resource group")
}

func TestPromptService_PromptSubscriptionResource_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.PromptSubscriptionResource(t.Context(), &azdext.PromptSubscriptionResourceRequest{})
	require.Error(t, err)
}

func TestPromptService_PromptSubscriptionResource_NoPrompt_DefaultResourceMessage(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.PromptSubscriptionResource(t.Context(), &azdext.PromptSubscriptionResourceRequest{
		Options: &azdext.PromptResourceOptions{
			ResourceTypeDisplayName: "OpenAI account",
		},
	})
	require.Error(t, err)
	requirePromptRequiredError(t, err, "Select OpenAI account")
}

func TestPromptService_PromptResourceGroupResource_NilAzureContext(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, false)
	_, err := svc.PromptResourceGroupResource(t.Context(), &azdext.PromptResourceGroupResourceRequest{})
	require.Error(t, err)
}

func TestPromptService_PromptResourceGroupResource_NoPrompt_UsesSelectOptionsMessage(t *testing.T) {
	t.Parallel()
	svc := newTestPromptService(&mockPromptService{}, true)
	_, err := svc.PromptResourceGroupResource(t.Context(), &azdext.PromptResourceGroupResourceRequest{
		Options: &azdext.PromptResourceOptions{
			SelectOptions: &azdext.PromptResourceSelectOptions{
				Message: "Select existing web app",
			},
		},
	})
	require.Error(t, err)
	requirePromptRequiredError(t, err, "Select existing web app")
}
