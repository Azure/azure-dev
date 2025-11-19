// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

var (
	ErrTemplateNotFound = fmt.Errorf("template not found")
)

type TemplateManager struct {
	sourceManager SourceManager
	sources       []Source
	console       input.Console
}

func NewTemplateManager(sourceManager SourceManager, console input.Console) (*TemplateManager, error) {
	return &TemplateManager{
		sourceManager: sourceManager,
		console:       console,
	}, nil
}

type ListOptions struct {
	Source string
	Tags   []string
}

type sourceFilterPredicate func(config *SourceConfig) bool
type templateFilterPredicate func(template *Template) bool

// ListTemplates retrieves the list of templates in a deterministic order.
func (tm *TemplateManager) ListTemplates(ctx context.Context, options *ListOptions) ([]*Template, error) {
	msg := "Retrieving templates..."
	tm.console.ShowSpinner(ctx, msg, input.Step)
	defer tm.console.StopSpinner(ctx, "", input.StepDone)

	allTemplates := []*Template{}

	var sourceFilterPredicate sourceFilterPredicate
	if options != nil && options.Source != "" {
		sourceFilterPredicate = func(config *SourceConfig) bool {
			return strings.EqualFold(config.Key, options.Source)
		}
	}

	var templateFilterPredicate templateFilterPredicate
	if options != nil && len(options.Tags) > 0 {
		// Find templates that match all the incoming tags
		templateFilterPredicate = func(template *Template) bool {
			match := false
			for _, optionTag := range options.Tags {
				match = slices.ContainsFunc(template.Tags, func(templateTag string) bool {
					return strings.EqualFold(optionTag, templateTag)
				})

				if !match {
					break
				}
			}

			return match
		}
	}

	sources, err := tm.getSources(ctx, sourceFilterPredicate)
	if err != nil {
		return nil, fmt.Errorf("failed listing templates: %w", err)
	}

	for _, source := range sources {
		filteredTemplates := []*Template{}
		sourceTemplates, err := source.ListTemplates(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to list templates: %w", err)
		}

		for _, template := range sourceTemplates {
			if templateFilterPredicate == nil || templateFilterPredicate(template) {
				filteredTemplates = append(filteredTemplates, template)
			}
		}

		// Sort by source, then repository path and finally name
		slices.SortFunc(filteredTemplates, func(a *Template, b *Template) int {
			if a.Source != b.Source {
				return strings.Compare(a.Source, b.Source)
			}

			if a.RepositoryPath != b.RepositoryPath {
				return strings.Compare(a.RepositoryPath, b.RepositoryPath)
			}

			return strings.Compare(a.Name, b.Name)
		})

		allTemplates = append(allTemplates, filteredTemplates...)
	}

	return allTemplates, nil
}

func (tm *TemplateManager) GetTemplate(ctx context.Context, path string) (*Template, error) {
	sources, err := tm.getSources(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed getting template sources: %w", err)
	}

	var match *Template
	var sourceErr error

	for _, source := range sources {
		template, err := source.GetTemplate(ctx, path)
		if err != nil {
			sourceErr = err
		} else if template != nil {
			match = template
			break
		}
	}

	if match != nil {
		return match, nil
	}

	if sourceErr != nil {
		return nil, fmt.Errorf("failed getting template: %w", sourceErr)
	}

	return nil, ErrTemplateNotFound
}

func (tm *TemplateManager) getSources(ctx context.Context, filter sourceFilterPredicate) ([]Source, error) {
	if tm.sources != nil {
		return tm.sources, nil
	}

	configs, err := tm.sourceManager.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed parsing template sources: %w", err)
	}

	sources, err := tm.createSourcesFromConfig(ctx, configs, filter)
	if err != nil {
		return nil, fmt.Errorf("failed initializing template sources: %w", err)
	}

	tm.sources = sources

	return tm.sources, nil
}

func (tm *TemplateManager) createSourcesFromConfig(
	ctx context.Context,
	configs []*SourceConfig,
	filter sourceFilterPredicate,
) ([]Source, error) {
	sources := []Source{}

	for _, config := range configs {
		if filter != nil && !filter(config) {
			continue
		}

		source, err := tm.sourceManager.CreateSource(ctx, config)
		if err != nil {
			log.Printf("failed to create source: %v", err)
			continue
		}

		sources = append(sources, source)
	}

	return sources, nil
}

// PromptTemplate asks the user to select a template.
func PromptTemplate(
	ctx context.Context,
	message string,
	templateManager *TemplateManager,
	console input.Console,
	options *ListOptions,
) (Template, error) {
	templates, err := templateManager.ListTemplates(ctx, options)
	if err != nil {
		return Template{}, fmt.Errorf("prompting for template: %w", err)
	}

	slices.SortFunc(templates, func(a, b *Template) int {
		return strings.Compare(a.Name, b.Name)
	})

	choices := make([]string, 0, len(templates)+1)
	for i, template := range templates {
		choice := template.Name
		if len(choice) > 70 {
			choice = choice[0:67] + "..."
		}

		for j, otherTemplate := range templates {
			if i != j && strings.EqualFold(template.Name, otherTemplate.Name) {
				// Disambiguate duplicate template names with source identifier
				choice += fmt.Sprintf(" (%s)", template.Source)
			}
		}

		languages := template.DisplayLanguages()
		path := template.CanonicalPath()

		choices = append(choices, fmt.Sprintf("%s\t%s\t[%s]",
			choice,
			path,
			strings.Join(languages, ",")))
	}

	choices, err = output.TabAlign(choices, 3)
	if err != nil {
		return Template{}, fmt.Errorf("aligning choices: %w", err)
	}

	selected, err := console.Select(ctx, input.ConsoleOptions{
		Message: message,
		Options: choices,
	})

	// separate this prompt from the next log
	console.Message(ctx, "")

	if err != nil {
		return Template{}, fmt.Errorf("prompting for template: %w", err)
	}

	template := templates[selected]
	log.Printf("Selected template: %s", fmt.Sprint(template.RepositoryPath))

	return *template, nil
}
