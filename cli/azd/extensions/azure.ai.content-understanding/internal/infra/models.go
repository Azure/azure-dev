// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/localio"
)

// Model is the selected live catalog entry and its canonical Bicep representation.
type Model struct {
	Name     string `json:"name"`
	Model    string `json:"model"`
	Version  string `json:"version"`
	Format   string `json:"format"`
	SKU      string `json:"skuName"`
	Capacity int32  `json:"skuCapacity"`
	Kind     string `json:"-"`
	Default  bool   `json:"-"`
}

func (m Model) Selector() string { return m.Name + "@" + m.Version }

// CatalogModel contains available ARM metadata before CU intersection and SKU choice.
type CatalogModel struct {
	Name, Version, Format string
	Default               bool
	SKUs                  []ModelSKU
}

type ModelSKU struct {
	Name     string
	Capacity int32
}

// ModelCapabilities separates CU policy from host prompts and ARM deployment.
type ModelCapabilities interface {
	Supported(context.Context) (cuclient.Analyzer, error)
	Catalog(context.Context) ([]CatalogModel, error)
	PromptModels(context.Context, []Model) ([]Model, error)
	Deploy(context.Context, []Model) error
	ConfigureDefaults(context.Context, map[string]string) error
}

// ModelResult reports side effects only after they have completed.
type ModelResult struct {
	Models             []Model `json:"models"`
	OutputFile         string  `json:"outputFile"`
	Deployed           bool    `json:"deployed"`
	DefaultsConfigured *bool   `json:"defaultsConfigured,omitempty"`
}

// Candidates intersects live CU families with live ARM versions and deterministic SKU choices.
func Candidates(analyzer cuclient.Analyzer, catalog []CatalogModel) ([]Model, error) {
	var wire struct {
		Supported *struct {
			Completion []string `json:"completion"`
			Embedding  []string `json:"embedding"`
		} `json:"supportedModels"`
	}
	if json.Unmarshal(analyzer.JSON, &wire) != nil || wire.Supported == nil ||
		(len(wire.Supported.Completion) == 0 && len(wire.Supported.Embedding) == 0) {
		return nil, &cuclient.Error{Kind: cuclient.ErrorService, Code: "invalid_supported_models",
			Message: "prebuilt-document did not return a valid supportedModels catalog."}
	}
	kinds := map[string]string{}
	for kind, names := range map[string][]string{
		"completion": wire.Supported.Completion, "embedding": wire.Supported.Embedding,
	} {
		for _, name := range names {
			kinds[strings.ToLower(strings.TrimSpace(name))] = kind
		}
	}
	result := []Model{}
	for _, row := range catalog {
		kind := kinds[strings.ToLower(row.Name)]
		if kind == "" || row.Name == "" || row.Version == "" || row.Format == "" || len(row.SKUs) == 0 {
			continue
		}
		skus := slices.Clone(row.SKUs)
		rank := func(name string) int {
			for i, preferred := range []string{"GlobalStandard", "DataZoneStandard", "Standard"} {
				if strings.EqualFold(name, preferred) {
					return i
				}
			}
			return 3
		}
		slices.SortFunc(skus, func(a, b ModelSKU) int {
			if diff := rank(a.Name) - rank(b.Name); diff != 0 {
				return diff
			}
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
		if skus[0].Name == "" {
			continue
		}
		result = append(result, Model{Name: row.Name, Model: row.Name, Version: row.Version, Format: row.Format,
			Kind: kind, SKU: skus[0].Name, Capacity: max(skus[0].Capacity, 1), Default: row.Default})
	}
	slices.SortFunc(result, func(a, b Model) int {
		for _, pair := range [][2]string{{a.Kind, b.Kind}, {strings.ToLower(a.Name), strings.ToLower(b.Name)},
			{a.Version, b.Version}} {
			if order := strings.Compare(pair[0], pair[1]); order != 0 {
				return order
			}
		}
		return 0
	})
	return result, nil
}

// SelectModels never guesses between multiple versions or deploys a duplicate model family.
func SelectModels(candidates []Model, selection string) ([]Model, error) {
	selected := []Model{}
	if selection == "recommended" {
		for _, preference := range []struct {
			kind  string
			names []string
		}{
			{"completion", []string{"gpt-5.2", "gpt-5.1", "gpt-5", "gpt-5-mini"}},
			{"embedding", []string{"text-embedding-3-large", "text-embedding-3-small", "text-embedding-ada-002"}},
		} {
			options := []Model{}
			for _, model := range candidates {
				if model.Kind == preference.kind {
					options = append(options, model)
				}
			}
			for _, name := range preference.names {
				family := []Model{}
				defaults := []Model{}
				for _, model := range options {
					if strings.EqualFold(name, model.Name) {
						family = append(family, model)
						if model.Default {
							defaults = append(defaults, model)
						}
					}
				}
				if len(family) == 0 {
					continue
				}
				if len(defaults) == 1 {
					options = defaults
				} else {
					options = family
				}
				break
			}
			if len(options) > 1 {
				return nil, invalid("ambiguous_model_version",
					"Select an explicit model@version; recommendation is ambiguous.")
			}
			selected = append(selected, options...)
		}
		if len(selected) == 0 {
			return nil, invalid("no_deployable_models", "No CU-supported model is deployable on this account.")
		}
		return selected, nil
	}
	seen := map[string]bool{}
	for selector := range strings.SplitSeq(selection, ",") {
		matches := []Model{}
		for _, model := range candidates {
			match := model.Name
			if strings.Contains(selector, "@") {
				match = model.Selector()
			}
			if strings.EqualFold(match, strings.TrimSpace(selector)) {
				matches = append(matches, model)
			}
		}
		if len(matches) != 1 {
			return nil, invalid("invalid_model_selector",
				"Select exactly one supported deployable model@version per family.")
		}
		model := matches[0]
		if seen[strings.ToLower(model.Name)] {
			return nil, invalid("duplicate_model_family", "A model family was selected more than once.")
		}
		seen[strings.ToLower(model.Name)] = true
		selected = append(selected, model)
	}
	return selected, nil
}

// SetupModels performs explicit deployment before saving the canonical Bicep input.
func SetupModels(
	ctx context.Context, capabilities ModelCapabilities, selection, output string, deploy, configure bool,
) (ModelResult, error) {
	result := ModelResult{Models: []Model{}, OutputFile: output}
	selection, err := NormalizeSelection(selection)
	if err != nil {
		return result, err
	}
	if output == "" {
		return result, invalid("missing_model_output", "Specify the model output file.")
	}
	if _, err := localio.Check(output); err != nil {
		return result, invalid("unsafe_model_output", "The model output or its parent is unsafe.")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if selection != "none" {
		analyzer, err := capabilities.Supported(ctx)
		if err != nil {
			return result, err
		}
		catalog, err := capabilities.Catalog(ctx)
		if err != nil {
			return result, err
		}
		candidates, err := Candidates(analyzer, catalog)
		if err != nil {
			return result, err
		}
		if len(candidates) == 0 {
			return result, invalid("no_deployable_models", "No live CU-supported models are deployable on this account.")
		}
		var selected []Model
		if selection == "prompt" {
			selected, err = capabilities.PromptModels(ctx, candidates)
		} else {
			selected, err = SelectModels(candidates, selection)
		}
		if err != nil {
			return result, err
		}
		result.Models = append(result.Models, selected...)
		configured := false
		result.DefaultsConfigured = &configured
		if deploy {
			if err := capabilities.Deploy(ctx, selected); err != nil {
				return result, err
			}
			result.Deployed = true
			if configure && len(selected) > 0 {
				mappings := map[string]string{}
				for _, model := range selected {
					mappings[model.Name] = model.Name
				}
				if err := capabilities.ConfigureDefaults(ctx, mappings); err != nil {
					return result, err
				}
				configured = true
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	body, err := json.MarshalIndent(result.Models, "", "  ")
	if err != nil {
		return result, err
	}
	if err := localio.Write(output, append(body, '\n'), true); err != nil {
		return result, &cuclient.Error{Kind: cuclient.ErrorService, Code: "model_file_write_failed",
			Message: fmt.Sprintf("The model file could not be saved; deployed=%t.", result.Deployed), Cause: err}
	}
	return result, nil
}
