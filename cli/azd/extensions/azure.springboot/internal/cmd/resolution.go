// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"

	"azure.springboot/internal/spring"
)

type initPrompter interface {
	Confirm(context.Context, string) (bool, error)
	Decide(context.Context, spring.Decision) (string, error)
	Close()
}

type hostPrompter struct {
	client  *azdext.AzdClient
	service azdext.PromptServiceClient
}

func newHostPrompter() (initPrompter, error) {
	client, err := azdext.NewAzdClient()
	if err != nil {
		return nil, err
	}
	return &hostPrompter{client: client, service: client.Prompt()}, nil
}

func (p *hostPrompter) Close() { p.client.Close() }

func (p *hostPrompter) Confirm(ctx context.Context, message string) (bool, error) {
	response, err := p.service.Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{Message: message, DefaultValue: new(false)},
	})
	if err != nil {
		return false, fmt.Errorf("requesting confirmation: %w", err)
	}
	if response == nil || response.Value == nil {
		return false, errors.New("confirmation returned no answer")
	}
	return response.GetValue(), nil
}

func (p *hostPrompter) Decide(ctx context.Context, decision spring.Decision) (string, error) {
	message := "Enter deployment value for " + decision.Input
	if decision.Profiles {
		message = "Select Spring profiles for the Azure deployment"
		if len(decision.Choices) != 0 {
			choices := make([]*azdext.SelectChoice, 0, len(decision.Choices)+1)
			for _, name := range decision.Choices {
				choices = append(choices, &azdext.SelectChoice{Value: name, Label: name})
			}
			choices = append(choices, &azdext.SelectChoice{Label: "Enter profiles manually"})
			response, err := p.service.Select(ctx, &azdext.SelectRequest{
				Options: &azdext.SelectOptions{Message: message, Choices: choices},
			})
			if err != nil {
				return "", fmt.Errorf("selecting deployment profiles: %w", err)
			}
			if response == nil || response.Value == nil ||
				response.GetValue() < 0 || int(response.GetValue()) >= len(choices) {
				return "", errors.New("profile selection returned no valid answer")
			}
			if index := int(response.GetValue()); index < len(decision.Choices) {
				return decision.Choices[index], nil
			}
		}
		message = "Enter Spring deployment profiles (comma-separated)"
	}
	var evidence []string
	secret := false
	for _, item := range decision.Evidence {
		evidence = append(evidence, item.Property+" in "+item.Source)
		secret = secret || item.Property == "spring.datasource.url"
	}
	response, err := p.service.Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message: message, HelpMessage: strings.Join(evidence, "; "), Required: true, Secret: secret,
		},
	})
	if err != nil {
		return "", fmt.Errorf("requesting deployment input: %w", err)
	}
	if response == nil || strings.TrimSpace(response.Value) == "" {
		return "", errors.New("deployment input returned no value")
	}
	return response.Value, nil
}

func confirm(
	ctx context.Context, getPrompts func() (initPrompter, error), message string,
) error {
	prompts, err := getPrompts()
	if err != nil {
		return err
	}
	accepted, err := prompts.Confirm(ctx, message)
	if err != nil {
		return err
	}
	if !accepted {
		return errors.New("initialization cancelled; no deployment files were written")
	}
	return nil
}

type cachedResolver struct {
	source spring.ModelResolver
	model  *spring.ModelData
}

func (r *cachedResolver) Resolve(ctx context.Context, directory string) (*spring.ModelData, error) {
	if r.model == nil {
		model, err := r.source.Resolve(ctx, directory)
		if err != nil {
			return nil, err
		}
		r.model = model
	}
	return r.model, nil
}

func resolveDeployment(
	ctx context.Context, directory string, resolver spring.ModelResolver,
	inputs spring.DeploymentInputs, noPrompt bool, getPrompts func() (initPrompter, error),
) (*spring.Application, error) {
	cached := &cachedResolver{source: resolver}
	answered := map[string]bool{}
	type repairTarget struct{ file, property string }
	repairedCredentials := map[repairTarget]bool{}
	for range 32 {
		analysis, err := spring.AnalyzeProject(ctx, directory, cached, inputs)
		if err != nil {
			return nil, err
		}
		if len(analysis.Diagnostics) != 0 {
			if len(analysis.Diagnostics) == 1 && len(analysis.Diagnostics[0].Repairs) != 0 &&
				!noPrompt {
				diagnostic := analysis.Diagnostics[0]
				var targets []string
				for _, repair := range diagnostic.Repairs {
					if repairedCredentials[repairTarget{repair.File, repair.Property}] {
						return nil, fmt.Errorf("credential property %s in %s requires repair again; "+
							"review the application configuration", repair.Property, repair.File)
					}
					targets = append(targets, repair.Property+" in "+repair.File)
				}
				question := "Replace credential defaults with runtime-only placeholders?\n  " +
					strings.Join(targets, "\n  ")
				if err := confirm(ctx, getPrompts, question); err != nil {
					return nil, err
				}
				if err := spring.ApplyCredentialRepairs(ctx, directory, diagnostic.Repairs); err != nil {
					return nil, err
				}
				for _, repair := range diagnostic.Repairs {
					repairedCredentials[repairTarget{repair.File, repair.Property}] = true
				}
				continue
			}
			var diagnostics []error
			for _, diagnostic := range analysis.Diagnostics {
				diagnostics = append(diagnostics, fmt.Errorf("%s configuration: %s", diagnostic.Kind, diagnostic.Message))
			}
			return nil, errors.Join(diagnostics...)
		}
		if len(analysis.Decisions) == 0 {
			return analysis.ResolvedApplication()
		}
		decision := analysis.Decisions[0]
		hint := "configure " + decision.Input + " in the application's Spring configuration"
		if len(decision.Evidence) != 0 {
			evidence := decision.Evidence[0]
			hint += fmt.Sprintf(" (referenced by %s in %s)", evidence.Property, evidence.Source)
		}
		if decision.Profiles {
			hint = "supply --profiles <names>"
		}
		if noPrompt || answered[decision.ID] {
			return nil, fmt.Errorf("deployment decision %s is unresolved; %s", decision.ID, hint)
		}
		prompts, err := getPrompts()
		if err != nil {
			return nil, fmt.Errorf("deployment decision %s is unresolved; %s: %w", decision.ID, hint, err)
		}
		answer, err := prompts.Decide(ctx, decision)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(answer) == "" {
			return nil, errors.New("deployment decision requires a nonempty answer")
		}
		if decision.Profiles {
			inputs.Profiles = strings.Split(answer, ",")
			for i := range inputs.Profiles {
				inputs.Profiles[i] = strings.TrimSpace(inputs.Profiles[i])
			}
		} else {
			if inputs.Properties == nil {
				inputs.Properties = map[string]string{}
			}
			inputs.Properties[decision.Input] = answer
		}
		answered[decision.ID] = true
	}
	return nil, errors.New("deployment resolution exceeds 32 dependent decisions")
}
