// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/infra"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type generationPrompts struct {
	environment, location, prefix, roles bool
}

func invalidInfraPrompt() error {
	return commandError("invalid_prompt_response", "The azd host returned an invalid infrastructure selection.",
		"Retry through azd or use explicit inputs with --no-prompt.")
}

func (h *azdInfraHost) PromptGeneration(
	ctx context.Context, choices infra.Choices, prompts generationPrompts,
) (infra.Choices, error) {
	for _, field := range []struct {
		enabled bool
		message string
		help    string
		value   *string
	}{
		{prompts.environment, "azd environment name",
			"Generate files only; run azd up afterwards. This name identifies the deployment under .azure/<name>.",
			&choices.Environment},
		{prompts.prefix, "Optional Microsoft Foundry resource name prefix (blank for generated name)",
			"The globally unique resource name becomes part of its public endpoint. Use 1-20 letters, numbers or hyphens.",
			&choices.FoundryPrefix},
	} {
		if !field.enabled {
			continue
		}
		help := field.help
		for {
			if err := ctx.Err(); err != nil {
				return choices, err
			}
			response, err := h.client.Prompt().Prompt(ctx, &azdext.PromptRequest{Options: &azdext.PromptOptions{
				Message: field.message, HelpMessage: help, DefaultValue: *field.value,
				Required: field.value == &choices.Environment,
			}})
			if err != nil {
				return choices, err
			}
			if response == nil {
				return choices, invalidInfraPrompt()
			}
			*field.value = response.GetValue()
			normalized, err := choices.Normalize()
			if err != nil {
				help = field.help + "\nInvalid input: " + err.Error()
				continue
			}
			choices = normalized
			break
		}
	}
	if prompts.location {
		options := &azdext.SelectOptions{
			Message:     "Select a Content Understanding region",
			HelpMessage: "Region for the new Microsoft Foundry resource.", EnableFiltering: new(true),
		}
		index := int32(0)
		for _, region := range infra.Regions {
			if region == choices.Location {
				options.SelectedIndex = new(index)
			}
			options.Choices = append(options.Choices, &azdext.SelectChoice{Value: region, Label: region})
			index++
		}
		response, err := h.client.Prompt().Select(ctx, &azdext.SelectRequest{Options: options})
		if err != nil {
			return choices, err
		}
		if response == nil || response.Value == nil || response.GetValue() < 0 ||
			int(response.GetValue()) >= len(infra.Regions) {
			return choices, invalidInfraPrompt()
		}
		choices.Location = infra.Regions[response.GetValue()]
	}
	if prompts.roles {
		response, err := h.client.Prompt().Confirm(ctx, &azdext.ConfirmRequest{Options: &azdext.ConfirmOptions{
			Message: "Assign Cognitive Services User to your user on the new Foundry resource?",
			HelpMessage: "Requires permission to create role assignments, such as Owner, User Access Administrator, " +
				"or Role Based Access Control Administrator. If skipped, ask an administrator to grant access for Entra ID.",
			DefaultValue: new(false),
		}})
		if err != nil {
			return choices, err
		}
		if response == nil || response.Value == nil {
			return choices, invalidInfraPrompt()
		}
		choices.AssignRoles = response.GetValue()
	}
	if err := ctx.Err(); err != nil {
		return choices, err
	}
	return choices.Normalize()
}

func (h *azdInfraHost) promptModels(ctx context.Context, models []infra.Model) ([]infra.Model, error) {
	options := &azdext.MultiSelectOptions{
		Message: "Select live CU-supported models to deploy",
		HelpMessage: "Accept or change the recommended selections. Clear all selections to deploy no models. " +
			"Select only one version of each model family.",
		AllowEmptySelection: new(true), EnableFiltering: new(true),
	}
	recommended, err := infra.SelectModels(models, "recommended")
	if err != nil {
		local, ok := errors.AsType[*cuclient.Error](err)
		if !ok || (local.Code != "ambiguous_model_version" && local.Code != "no_deployable_models") {
			return nil, err
		}
		options.HelpMessage += "\nNo models preselected: " + local.Message
	}
	for _, model := range models {
		label := fmt.Sprintf("%s | %s | %s", model.Selector(), model.Kind, model.SKU)
		isRecommended := slices.Contains(recommended, model)
		if isRecommended {
			label += " (recommended)"
		}
		options.Choices = append(options.Choices, &azdext.MultiSelectChoice{
			Value: model.Selector(), Label: label, Selected: isRecommended,
		})
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response, err := h.client.Prompt().MultiSelect(ctx, &azdext.MultiSelectRequest{Options: options})
		if err != nil {
			return nil, err
		}
		if response == nil {
			return nil, invalidInfraPrompt()
		}
		selectors := []string{}
		for _, choice := range response.GetValues() {
			if choice == nil {
				return nil, invalidInfraPrompt()
			}
			selectors = append(selectors, choice.GetValue())
		}
		if len(selectors) == 0 {
			return []infra.Model{}, ctx.Err()
		}
		selected, err := infra.SelectModels(models, strings.Join(selectors, ","))
		if err == nil {
			// A host without a terminal can accept preselected choices at EOF; require an explicit opt-in to deploy.
			confirmation, err := h.client.Prompt().Confirm(ctx, &azdext.ConfirmRequest{Options: &azdext.ConfirmOptions{
				Message:      "Deploy these models: " + strings.Join(selectors, ", ") + "?",
				HelpMessage:  "Model deployment can incur Azure charges. No models will be deployed unless you confirm.",
				DefaultValue: new(false),
			}})
			if err != nil {
				return nil, err
			}
			if confirmation == nil || confirmation.Value == nil {
				return nil, invalidInfraPrompt()
			}
			if !confirmation.GetValue() {
				return nil, context.Canceled
			}
			return selected, ctx.Err()
		}
		options.HelpMessage = "Invalid selection: " + err.Error() +
			" Select only one version of each family, or clear all selections for no models."
		for _, choice := range options.Choices {
			choice.Selected = slices.Contains(selectors, choice.Value)
		}
	}
}
