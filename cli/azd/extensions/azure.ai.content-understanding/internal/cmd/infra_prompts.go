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
				if err := writeGuidance(reason); err != nil {
					return err
				}
				help = guidance + "\n" + reason
				continue
			}
			choices = normalized
			return nil
		}
	}
	if err := promptText(prompts.environment, "azd environment name",
		"Environment name\n"+
			"azd stores this deployment's configuration and outputs under .azure/<environment>/ "+
			"in the generated directory.\n"+
			"The name seeds resource names such as rg-<environment> and proj-<environment>, "+
			"and separates stacks such as dev, test, and prod.\n"+
			"Use 1-64 letters, numbers, hyphens, underscores, periods, or parentheses.",
		&choices.Environment, "invalid_environment"); err != nil {
		return choices, err
	}
	if prompts.location {
		if err := writeGuidance("Azure region\n" +
			"This is where the new Microsoft Foundry resource will be created. " +
			"Model availability and quota vary by region.\n" +
			"Check supported regions: https://learn.microsoft.com/azure/ai-services/content-understanding/" +
			"language-region-support"); err != nil {
			return choices, err
		}
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
	if err := promptText(prompts.prefix, "Optional Microsoft Foundry resource name prefix (blank for generated name)",
		"Microsoft Foundry resource naming\n"+
			"The resource name forms its public endpoint, https://<resource-name>.services.ai.azure.com, "+
			"and must be globally unique.\n"+
			"A prefix produces <prefix>-<unique-suffix>; leave it blank for aif-<unique-suffix>.\n"+
			"Use 1-20 letters, numbers, or internal hyphens; the prefix is normalized to lowercase.",
		&choices.FoundryPrefix, "invalid_foundry_prefix"); err != nil {
		return choices, err
	}
	if prompts.roles {
		if err := writeGuidance("Data-plane access (RBAC)\n" +
			"Cognitive Services User grants Content Understanding data-plane access. " +
			"Contributor alone does not grant this access or permission to assign roles.\n" +
			"Assigning the role requires Owner, User Access Administrator, or " +
			"Role Based Access Control Administrator, in addition to deployment permissions.\n" +
			"The default is No. If you skip assignment, an administrator must grant access " +
			"unless you already have it. Provisioning and postprovision setup use Entra ID, not an API key."); err != nil {
			return choices, err
		}
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
