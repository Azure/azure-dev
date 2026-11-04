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
	writeGuidance := func(text string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(h.output, "\n%s\n", text); err != nil {
			return fmt.Errorf("write infrastructure guidance: %w", err)
		}
		return nil
	}
	promptText := func(enabled bool, message, guidance string, value *string, code string) error {
		if !enabled {
			return nil
		}
		if err := writeGuidance(guidance); err != nil {
			return err
		}
		help := guidance
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			response, err := h.client.Prompt().Prompt(ctx, &azdext.PromptRequest{Options: &azdext.PromptOptions{
				Message: message, HelpMessage: help, DefaultValue: *value,
				Required: value == &choices.Environment,
			}})
			if err != nil {
				return err
			}
			if response == nil {
				return invalidInfraPrompt()
			}
			*value = response.GetValue()
			normalized, err := choices.Normalize()
			if err != nil {
				local, ok := errors.AsType[*cuclient.Error](err)
				if !ok || local.Kind != cuclient.ErrorValidation || local.Code != code {
					return err
				}
				reason := "Invalid input: " + err.Error()
				if err := writeGuidance(errorText(h.outputFormat, reason)); err != nil {
					return err
				}
				help = guidance + "\n" + reason
				continue
			}
			choices = normalized
			return nil
		}
	}
	if err := promptText(prompts.environment,
		"Enter your environment name (1-64 letters, numbers, -, _, ., or parentheses)",
		"Input your azd environment name. An azd environment name:\n"+
			"  - lets azd store this deployment's config + outputs under .azure/<env>/ in the generated directory.\n"+
			"  - seeds your Azure resource names (e.g. rg-<env>, proj-<env>).\n"+
			"  - keeps separate stacks apart, such as dev, test, or prod.",
		&choices.Environment, "invalid_environment"); err != nil {
		return choices, err
	}
	if prompts.location {
		if err := writeGuidance("Content Understanding supported regions\n" +
			"Check the latest region support at https://learn.microsoft.com/azure/ai-services/content-understanding/" +
			"language-region-support"); err != nil {
			return choices, err
		}
		options := &azdext.SelectOptions{
			Message:     "Azure region (where the Foundry resource is created)",
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
	if err := promptText(prompts.prefix,
		"Optional Microsoft Foundry resource name prefix (lowercase letters, numbers, hyphen; blank to skip)",
		"Microsoft Foundry resource naming\n"+
			"The resource name becomes part of the public endpoint host "+
			"(for example, https://<name>.services.ai.azure.com), so it must be globally unique.\n"+
			"If you provide a prefix, azd constructs the resource name as <prefix>-<unique-suffix>. "+
			"Without a prefix, it uses aif-<unique-suffix>.",
		&choices.FoundryPrefix, "invalid_foundry_prefix"); err != nil {
		return choices, err
	}
	if prompts.roles {
		if err := writeGuidance(warningText(h.outputFormat, "RBAC roles\n"+
			"Required for Entra-based auth from `azd ai cu`. The template assigns Cognitive Services User.\n"+
			"Needs Owner, User Access Administrator, or Role Based Access Control Administrator on the subscription.\n"+
			"Pick 'n' if you only have Contributor. If you skip assignment, ask an administrator to grant access "+
			"unless you already have it.\n"+
			"Provisioning and post-provision setup use Entra ID, not an API key.")); err != nil {
			return choices, err
		}
		response, err := h.client.Prompt().Confirm(ctx, &azdext.ConfirmRequest{Options: &azdext.ConfirmOptions{
			Message: "Assign RBAC roles to your user on the Microsoft Foundry resource?",
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
	const message = "Select live CU-supported models to deploy"
	options := &azdext.MultiSelectOptions{
		Message: message,
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
		options.Message = warningText(h.outputFormat, "No models preselected: "+local.Message) + "\n" + message
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
		options.Message = errorText(h.outputFormat, "Invalid selection: "+err.Error()+
			" Select only one version of each family, or clear all selections for no models.") + "\n" + message
		for _, choice := range options.Choices {
			choice.Selected = slices.Contains(selectors, choice.Value)
		}
	}
}
