// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package inputs defines the preview coordination contract for missing azd inputs.
package inputs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
)

const (
	// EnvironmentNameKind identifies an azd environment name.
	EnvironmentNameKind = "azd.environment.name/v1"
	// AzureLocationKind identifies an Azure deployment location.
	AzureLocationKind = "azure.location/v1"
)

// Option is a combined candidate with its preferences and contributing sources.
type Option struct {
	Value       string
	Recommended bool
	Sources     []string
	Rationales  []string
}

// Contribution associates an extension's response with its authenticated identity.
type Contribution struct {
	Source   string
	Response *v1beta.ResolveInputResponse
}

// Resolver gathers and combines options without prompting or persisting values.
// An empty result means no extension contributed options.
type Resolver interface {
	ResolveInput(context.Context, *v1beta.InputDefinition) ([]Option, error)
}

// Combine intersects required sets, unions suggestions, and merges equal options.
// Empty intersections fail without disclosing candidate values.
func Combine(definition *v1beta.InputDefinition, contributions []Contribution) ([]Option, error) {
	options := make(map[string]Option)
	var requiredSets [][]string
	var constraintSources []string
	if len(definition.AllowedValues) > 0 {
		requiredSets = append(requiredSets, definition.AllowedValues)
		constraintSources = append(constraintSources, "input definition")
	}
	for _, contribution := range contributions {
		response := contribution.Response
		if response == nil {
			return nil, fmt.Errorf("extension %q returned an invalid input response", contribution.Source)
		}
		candidates := response.Options
		legacy := response.Value //nolint:staticcheck // Preserve the initial preview's singular wire response.
		if legacy != nil {
			if len(candidates) > 0 || response.Required {
				return nil, fmt.Errorf("extension %q mixed legacy and option responses", contribution.Source)
			}
			candidates = []*v1beta.InputOption{{Value: *legacy, Recommended: true}}
		}
		var allowed []string
		for _, candidate := range candidates {
			if candidate == nil {
				return nil, fmt.Errorf("extension %q returned an invalid input option", contribution.Source)
			}
			option := options[candidate.Value]
			option.Value = candidate.Value
			option.Recommended = option.Recommended || candidate.Recommended
			if !slices.Contains(option.Sources, contribution.Source) {
				option.Sources = append(option.Sources, contribution.Source)
			}
			if candidate.Rationale != "" && !slices.Contains(option.Rationales, candidate.Rationale) {
				option.Rationales = append(option.Rationales, candidate.Rationale)
			}
			options[candidate.Value] = option
			allowed = append(allowed, candidate.Value)
		}
		if response.Required {
			if len(allowed) == 0 {
				return nil, fmt.Errorf("extension %q requires an empty option set for input %q",
					contribution.Source, definition.Id)
			}
			requiredSets = append(requiredSets, allowed)
			constraintSources = append(constraintSources, contribution.Source)
		}
	}
	var result []Option
	for value, option := range options {
		if slices.ContainsFunc(requiredSets, func(allowed []string) bool {
			return !slices.Contains(allowed, value)
		}) {
			continue
		}
		slices.Sort(option.Sources)
		slices.Sort(option.Rationales)
		result = append(result, option)
	}
	if len(options) > 0 && len(result) == 0 {
		return nil, fmt.Errorf("no compatible options for input %q: constraints from %q conflict",
			definition.Id, constraintSources)
	}
	sortOptions(result)
	return result, nil
}

// Resolve validates combined options, accepts a sole option or unique recommendation,
// and otherwise prompts. The caller persists the selected value through its normal lifecycle.
// A filter returns false for invalid options and an error for failures that must stop resolution.
func Resolve(
	ctx context.Context,
	locator ioc.ServiceLocator,
	definition *v1beta.InputDefinition,
	filter func(string) (bool, error),
) (*string, error) {
	if locator == nil {
		return nil, nil
	}
	var resolver Resolver
	if err := locator.Resolve(&resolver); err != nil {
		if errors.Is(err, ioc.ErrResolveInstance) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolving input coordinator: %w", err)
	}
	options, err := resolver.ResolveInput(ctx, definition)
	if err != nil {
		return nil, fmt.Errorf("resolving input %q: %w", definition.GetId(), err)
	}
	if len(options) == 0 {
		return nil, nil
	}
	valid := make([]Option, 0, len(options))
	for _, option := range options {
		if len(definition.AllowedValues) > 0 && !slices.Contains(definition.AllowedValues, option.Value) {
			continue
		}
		if filter != nil {
			accepted, err := filter(option.Value)
			if err != nil {
				return nil, fmt.Errorf("validating input provider options for %q: %w", definition.Id, err)
			}
			if !accepted {
				continue
			}
		}
		valid = append(valid, option)
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("no valid input provider options remain for input %q", definition.Id)
	}
	sortOptions(valid)
	recommendations := 0
	for _, option := range valid {
		if option.Recommended {
			recommendations++
		}
	}
	var console input.Console
	if err := locator.Resolve(&console); err != nil {
		return nil, fmt.Errorf("resolving console for input selection: %w", err)
	}
	if len(valid) == 1 || recommendations == 1 {
		reason := "only valid option"
		if len(valid) > 1 {
			reason = "unique recommendation"
		}
		console.Message(ctx, fmt.Sprintf("Resolved input %q from extensions: %s (%s).",
			definition.Id, strings.Join(valid[0].Sources, ", "), reason))
		return new(valid[0].Value), nil
	}
	if console.IsNoPromptMode() {
		required := input.RequiredInput{
			Name: definition.Id, Description: "Multiple valid options remain without a unique recommendation.",
		}
		if definition.EnvironmentVariable != "" {
			required.Sources = []input.InputSource{{
				Kind: input.InputSourceEnvironment, Name: definition.EnvironmentVariable,
			}}
		}
		return nil, &input.PromptRequiredError{
			Message: "Input provider options require a selection.", Inputs: []input.RequiredInput{required},
		}
	}
	choices := make([]string, len(valid))
	details := make([]string, len(valid))
	for i, option := range valid {
		choices[i] = option.Value
		details[i] = strings.Join(option.Sources, ", ")
		if option.Recommended {
			choices[i] += " (recommended)"
		}
		if len(option.Rationales) > 0 {
			details[i] += ": " + strings.Join(option.Rationales, "; ")
		}
	}
	index, err := console.Select(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf("Select a value for %s:", definition.Id), Options: choices, OptionDetails: details,
	})
	if err != nil {
		return nil, fmt.Errorf("selecting input %q: %w", definition.Id, err)
	}
	if index < 0 || index >= len(valid) {
		return nil, fmt.Errorf("invalid selection for input %q", definition.Id)
	}
	return new(valid[index].Value), nil
}

func sortOptions(options []Option) {
	slices.SortFunc(options, func(a, b Option) int {
		if a.Recommended != b.Recommended {
			if a.Recommended {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Value, b.Value)
	})
}
