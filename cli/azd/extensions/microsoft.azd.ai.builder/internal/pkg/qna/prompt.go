// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package qna

import (
	"context"

	"github.com/azure/azure-dev/pkg/azdext"
)

type TextPrompt struct {
	Message           string
	HelpMessage       string
	Placeholder       string
	DefaultValue      string
	ValidationMessage string
	RequiredMessage   string
	Client            *azdext.AzdClient
}

func (p *TextPrompt) Ask(ctx context.Context, question Question) (any, error) {
	promptResponse, err := p.Client.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:           p.Message,
			HelpMessage:       p.HelpMessage,
			Placeholder:       p.Placeholder,
			ValidationMessage: p.ValidationMessage,
			RequiredMessage:   p.RequiredMessage,
			DefaultValue:      p.DefaultValue,
			Required:          true,
		},
	})
	if err != nil {
		return nil, err
	}

	return promptResponse.Value, nil
}

type SingleSelectPrompt struct {
	Message         string
	HelpMessage     string
	Choices         []Choice
	EnableFiltering *bool
	Client          *azdext.AzdClient
	BeforeAsk       func(ctx context.Context, q *Question, p *SingleSelectPrompt) error
}

func (p *SingleSelectPrompt) Ask(ctx context.Context, question Question) (any, error) {
	if p.BeforeAsk != nil {
		if err := p.BeforeAsk(ctx, &question, p); err != nil {
			return nil, err
		}
	}

	choices := make([]*azdext.SelectChoice, len(p.Choices))
	for i, choice := range p.Choices {
		choices[i] = &azdext.SelectChoice{
			Label: choice.Label,
			Value: choice.Value,
		}
	}

	selectResponse, err := p.Client.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         p.Message,
			HelpMessage:     p.HelpMessage,
			Choices:         choices,
			EnableFiltering: p.EnableFiltering,
		},
	})
	if err != nil {
		return nil, err
	}

	selectedChoice := p.Choices[*selectResponse.Value]

	return selectedChoice.Value, nil
}

type MultiSelectPrompt struct {
	Message         string
	HelpMessage     string
	Choices         []Choice
	EnableFiltering *bool
	Client          *azdext.AzdClient
	BeforeAsk       func(ctx context.Context, q *Question, p *MultiSelectPrompt) error
}

func (p *MultiSelectPrompt) Ask(ctx context.Context, question Question) (any, error) {
	if p.BeforeAsk != nil {
		if err := p.BeforeAsk(ctx, &question, p); err != nil {
			return nil, err
		}
	}

	choices := make([]*azdext.MultiSelectChoice, len(p.Choices))
	for i, choice := range p.Choices {
		choices[i] = &azdext.MultiSelectChoice{
			Label: choice.Label,
			Value: choice.Value,
		}
	}

	selectResponse, err := p.Client.Prompt().MultiSelect(ctx, &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message:         p.Message,
			Choices:         choices,
			HelpMessage:     p.HelpMessage,
			EnableFiltering: p.EnableFiltering,
		},
	})
	if err != nil {
		return nil, err
	}

	selectedChoices := make([]string, len(selectResponse.Values))
	for i, value := range selectResponse.Values {
		selectedChoices[i] = value.Value
	}

	return selectedChoices, nil
}

type ConfirmPrompt struct {
	Message      string
	DefaultValue *bool
	HelpMessage  string
	Placeholder  string
	Client       *azdext.AzdClient
}

func (p *ConfirmPrompt) Ask(ctx context.Context, question Question) (any, error) {
	confirmResponse, err := p.Client.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      p.Message,
			DefaultValue: p.DefaultValue,
			HelpMessage:  p.HelpMessage,
			Placeholder:  p.Placeholder,
		},
	})
	if err != nil {
		return nil, err
	}

	return *confirmResponse.Value, nil
}

type SubscriptionResourcePrompt struct {
	Message                 string
	HelpMessage             string
	ResourceType            string
	ResourceTypeDisplayName string
	Kinds                   []string
	AzureContext            *azdext.AzureContext
	Client                  *azdext.AzdClient
	BeforeAsk               func(ctx context.Context, q *Question, p *SubscriptionResourcePrompt) error
}

func (p *SubscriptionResourcePrompt) Ask(ctx context.Context, question Question) (any, error) {
	if p.BeforeAsk != nil {
		if err := p.BeforeAsk(ctx, &question, p); err != nil {
			return nil, err
		}
	}

	resourceResponse, err := p.Client.Prompt().PromptSubscriptionResource(ctx, &azdext.PromptSubscriptionResourceRequest{
		Options: &azdext.PromptResourceOptions{
			ResourceType:            p.ResourceType,
			Kinds:                   p.Kinds,
			ResourceTypeDisplayName: p.ResourceTypeDisplayName,
			SelectOptions: &azdext.PromptResourceSelectOptions{
				Message:     p.HelpMessage,
				HelpMessage: p.HelpMessage,
			},
		},
		AzureContext: p.AzureContext,
	})
	if err != nil {
		return nil, err
	}

	return resourceResponse.Resource.Id, nil
}
