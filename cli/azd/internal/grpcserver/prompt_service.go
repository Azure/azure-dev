// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
)

type promptService struct {
	azdext.UnimplementedPromptServiceServer
	prompter        prompt.PromptService
	resourceService *azapi.ResourceService
}

func NewPromptService(prompter prompt.PromptService, resourceService *azapi.ResourceService) azdext.PromptServiceServer {
	return &promptService{
		prompter:        prompter,
		resourceService: resourceService,
	}
}

func (s *promptService) Confirm(ctx context.Context, req *azdext.ConfirmRequest) (*azdext.ConfirmResponse, error) {
	options := &ux.ConfirmOptions{
		DefaultValue: req.Options.DefaultValue,
		Message:      req.Options.Message,
		HelpMessage:  req.Options.HelpMessage,
		Hint:         req.Options.Hint,
		PlaceHolder:  req.Options.Placeholder,
	}

	confirm := ux.NewConfirm(options)
	value, err := confirm.Ask(ctx)

	return &azdext.ConfirmResponse{
		Value: value,
	}, err
}

func (s *promptService) Select(ctx context.Context, req *azdext.SelectRequest) (*azdext.SelectResponse, error) {
	choices := make([]*ux.SelectChoice, len(req.Options.Choices))
	for i, choice := range req.Options.Choices {
		choices[i] = &ux.SelectChoice{
			Value: choice.Value,
			Label: choice.Label,
		}
	}

	options := &ux.SelectOptions{
		SelectedIndex:   convertToInt(req.Options.SelectedIndex),
		Message:         req.Options.Message,
		Choices:         choices,
		HelpMessage:     req.Options.HelpMessage,
		DisplayCount:    int(req.Options.DisplayCount),
		DisplayNumbers:  req.Options.DisplayNumbers,
		EnableFiltering: req.Options.EnableFiltering,
	}

	selectPrompt := ux.NewSelect(options)
	value, err := selectPrompt.Ask(ctx)

	return &azdext.SelectResponse{
		Value: convertToInt32(value),
	}, err
}

func (s *promptService) MultiSelect(
	ctx context.Context,
	req *azdext.MultiSelectRequest,
) (*azdext.MultiSelectResponse, error) {
	choices := make([]*ux.MultiSelectChoice, len(req.Options.Choices))
	for i, choice := range req.Options.Choices {
		choices[i] = &ux.MultiSelectChoice{
			Value:    choice.Value,
			Label:    choice.Label,
			Selected: choice.Selected,
		}
	}

	options := &ux.MultiSelectOptions{
		Message:         req.Options.Message,
		Choices:         choices,
		HelpMessage:     req.Options.HelpMessage,
		DisplayCount:    int(req.Options.DisplayCount),
		DisplayNumbers:  req.Options.DisplayNumbers,
		EnableFiltering: req.Options.EnableFiltering,
	}

	selectPrompt := ux.NewMultiSelect(options)
	values, err := selectPrompt.Ask(ctx)

	resultValues := make([]*azdext.MultiSelectChoice, len(values))
	for i, value := range values {
		resultValues[i] = &azdext.MultiSelectChoice{
			Value:    value.Value,
			Label:    value.Label,
			Selected: value.Selected,
		}
	}

	return &azdext.MultiSelectResponse{
		Values: resultValues,
	}, err
}

func (s *promptService) Prompt(ctx context.Context, req *azdext.PromptRequest) (*azdext.PromptResponse, error) {
	options := &ux.PromptOptions{
		DefaultValue:      req.Options.DefaultValue,
		Message:           req.Options.Message,
		HelpMessage:       req.Options.HelpMessage,
		Hint:              req.Options.Hint,
		PlaceHolder:       req.Options.Placeholder,
		ValidationMessage: req.Options.ValidationMessage,
		RequiredMessage:   req.Options.RequiredMessage,
		Required:          req.Options.Required,
		ClearOnCompletion: req.Options.ClearOnCompletion,
		IgnoreHintKeys:    req.Options.IgnoreHintKeys,
	}

	prompt := ux.NewPrompt(options)
	value, err := prompt.Ask(ctx)

	return &azdext.PromptResponse{
		Value: value,
	}, err
}

func (s *promptService) PromptSubscription(
	ctx context.Context,
	req *azdext.PromptSubscriptionRequest,
) (*azdext.PromptSubscriptionResponse, error) {
	selectedSubscription, err := s.prompter.PromptSubscription(ctx, &prompt.SelectOptions{
		Message:     req.Message,
		HelpMessage: req.HelpMessage,
	})
	if err != nil {
		return nil, err
	}

	subscription := &azdext.Subscription{
		Id:           selectedSubscription.Id,
		Name:         selectedSubscription.Name,
		TenantId:     selectedSubscription.TenantId,
		UserTenantId: selectedSubscription.UserAccessTenantId,
		IsDefault:    selectedSubscription.IsDefault,
	}

	return &azdext.PromptSubscriptionResponse{
		Subscription: subscription,
	}, nil
}

func (s *promptService) PromptLocation(
	ctx context.Context,
	req *azdext.PromptLocationRequest,
) (*azdext.PromptLocationResponse, error) {
	azureContext, err := s.createAzureContext(req.AzureContext)
	if err != nil {
		return nil, err
	}

	selectedLocation, err := s.prompter.PromptLocation(ctx, azureContext, nil)
	if err != nil {
		return nil, err
	}

	location := &azdext.Location{
		Name:                selectedLocation.Name,
		DisplayName:         selectedLocation.DisplayName,
		RegionalDisplayName: selectedLocation.RegionalDisplayName,
	}

	return &azdext.PromptLocationResponse{
		Location: location,
	}, nil
}

func (s *promptService) PromptResourceGroup(
	ctx context.Context,
	req *azdext.PromptResourceGroupRequest,
) (*azdext.PromptResourceGroupResponse, error) {
	azureContext, err := s.createAzureContext(req.AzureContext)
	if err != nil {
		return nil, err
	}

	selectedResourceGroup, err := s.prompter.PromptResourceGroup(ctx, azureContext, nil)
	if err != nil {
		return nil, err
	}

	resourceGroup := &azdext.ResourceGroup{
		Id:       selectedResourceGroup.Id,
		Name:     selectedResourceGroup.Name,
		Location: selectedResourceGroup.Location,
	}

	return &azdext.PromptResourceGroupResponse{
		ResourceGroup: resourceGroup,
	}, nil
}

func (s *promptService) PromptSubscriptionResource(
	ctx context.Context,
	req *azdext.PromptSubscriptionResourceRequest,
) (*azdext.PromptSubscriptionResourceResponse, error) {
	azureContext, err := s.createAzureContext(req.AzureContext)
	if err != nil {
		return nil, err
	}

	options := createResourceOptions(req.Options)

	resource, err := s.prompter.PromptSubscriptionResource(ctx, azureContext, options)
	if err != nil {
		return nil, err
	}

	return &azdext.PromptSubscriptionResourceResponse{
		Resource: &azdext.ResourceExtended{
			Id:       resource.Id,
			Name:     resource.Name,
			Type:     resource.Type,
			Location: resource.Location,
			Kind:     resource.Kind,
		},
	}, nil
}

func (s *promptService) PromptResourceGroupResource(
	ctx context.Context,
	req *azdext.PromptResourceGroupResourceRequest,
) (*azdext.PromptResourceGroupResourceResponse, error) {
	azureContext, err := s.createAzureContext(req.AzureContext)
	if err != nil {
		return nil, err
	}

	options := createResourceOptions(req.Options)

	resource, err := s.prompter.PromptResourceGroupResource(ctx, azureContext, options)
	if err != nil {
		return nil, err
	}

	return &azdext.PromptResourceGroupResourceResponse{
		Resource: &azdext.ResourceExtended{
			Id:       resource.Id,
			Name:     resource.Name,
			Type:     resource.Type,
			Location: resource.Location,
			Kind:     resource.Kind,
		},
	}, nil
}

func (s *promptService) createAzureContext(wire *azdext.AzureContext) (*prompt.AzureContext, error) {
	scope := prompt.AzureScope{
		TenantId:       wire.Scope.TenantId,
		SubscriptionId: wire.Scope.SubscriptionId,
		Location:       wire.Scope.Location,
		ResourceGroup:  wire.Scope.ResourceGroup,
	}

	resources := []*arm.ResourceID{}
	for _, resourceId := range wire.Resources {
		parsedResource, err := arm.ParseResourceID(resourceId)
		if err != nil {
			return nil, err
		}

		resources = append(resources, parsedResource)
	}

	resourceList := prompt.NewAzureResourceList(s.resourceService, resources)

	return prompt.NewAzureContext(s.prompter, scope, resourceList), nil
}

func createResourceOptions(options *azdext.PromptResourceOptions) prompt.ResourceOptions {
	if options == nil {
		return prompt.ResourceOptions{}
	}

	var resourceType *azapi.AzureResourceType
	if options.ResourceType != "" {
		resourceType = to.Ptr(azapi.AzureResourceType(options.ResourceType))
	}

	var selectOptions *prompt.SelectOptions

	if options.SelectOptions != nil {
		selectOptions = &prompt.SelectOptions{
			ForceNewResource:   options.SelectOptions.ForceNewResource,
			NewResourceMessage: options.SelectOptions.NewResourceMessage,
			Message:            options.SelectOptions.Message,
			HelpMessage:        options.SelectOptions.HelpMessage,
			LoadingMessage:     options.SelectOptions.LoadingMessage,
			DisplayCount:       int(options.SelectOptions.DisplayCount),
			DisplayNumbers:     options.SelectOptions.DisplayNumbers,
			AllowNewResource:   options.SelectOptions.AllowNewResource,
		}
	}

	resourceOptions := prompt.ResourceOptions{
		ResourceType:            resourceType,
		Kinds:                   options.Kinds,
		ResourceTypeDisplayName: options.ResourceTypeDisplayName,
		SelectorOptions:         selectOptions,
	}

	return resourceOptions
}

func convertToInt32(input *int) *int32 {
	if input == nil {
		return nil // Handle the nil case
	}

	// nolint:gosec // G115
	value := int32(*input) // Convert the dereferenced value to int32
	return &value          // Return the address of the new int32 value
}

func convertToInt(input *int32) *int {
	if input == nil {
		return nil // Handle the nil case
	}
	value := int(*input) // Convert the dereferenced value to int
	return &value        // Return the address of the new int value
}
