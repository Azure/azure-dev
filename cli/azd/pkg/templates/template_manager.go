package templates

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"golang.org/x/exp/slices"
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
}

type sourceFilterPredicate func(config *SourceConfig) bool

// ListTemplates retrieves the list of templates in a deterministic order.
func (tm *TemplateManager) ListTemplates(ctx context.Context, options *ListOptions) ([]*Template, error) {
	msg := "Retrieving templates..."
	tm.console.ShowSpinner(ctx, msg, input.Step)
	defer tm.console.StopSpinner(ctx, "", input.StepDone)

	allTemplates := []*Template{}

	var filterPredicate sourceFilterPredicate
	if options != nil && options.Source != "" {
		filterPredicate = func(config *SourceConfig) bool {
			return strings.EqualFold(config.Key, options.Source)
		}
	}

	sources, err := tm.getSources(ctx, filterPredicate)
	if err != nil {
		return nil, fmt.Errorf("failed listing templates: %w", err)
	}

	for _, source := range sources {
		templates, err := source.ListTemplates(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to list templates: %w", err)
		}

		// Sort by source, then repository path and finally name
		slices.SortFunc(templates, func(a *Template, b *Template) bool {
			if a.Source != b.Source {
				return a.Source < b.Source
			}

			if a.RepositoryPath != b.RepositoryPath {
				return a.RepositoryPath < b.RepositoryPath
			}

			return a.Name < b.Name
		})

		allTemplates = append(allTemplates, templates...)
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
			log.Printf("failed to create source: %s", err.Error())
			continue
		}

		sources = append(sources, source)
	}

	return sources, nil
}

// PromptTemplate asks the user to select a template.
// An empty Template can be returned if the user selects the minimal template. This corresponds to the minimal azd template.
// See
func PromptTemplate(
	ctx context.Context,
	message string,
	templateManager *TemplateManager,
	console input.Console,
) (*Template, error) {
	templates, err := templateManager.ListTemplates(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("prompting for template: %w", err)
	}

	templateChoices := []*Template{}
	duplicateNames := []string{}

	// Check for duplicate template names
	for _, template := range templates {
		hasDuplicateName := slices.ContainsFunc(templateChoices, func(t *Template) bool {
			return t.Name == template.Name
		})

		if hasDuplicateName {
			duplicateNames = append(duplicateNames, template.Name)
		}

		templateChoices = append(templateChoices, template)
	}

	templateNames := make([]string, 0, len(templates)+1)
	templateDetails := make([]string, 0, len(templates)+1)

	// Prepend the minimal template option to guarantee first selection
	minimalChoice := "Minimal"

	templateNames = append(templateNames, minimalChoice)
	templateDetails = append(templateDetails, "")

	for _, template := range templates {
		templateChoice := template.Name

		// Disambiguate duplicate template names with source identifier
		if slices.Contains(duplicateNames, template.Name) {
			templateChoice += fmt.Sprintf(" (%s)", template.Source)
		}

		templateDetails = append(templateDetails, template.RepositoryPath)

		if slices.Contains(templateNames, templateChoice) {
			duplicateNames = append(duplicateNames, templateChoice)
		}

		templateNames = append(templateNames, templateChoice)
	}

	selected, err := console.Select(ctx, input.ConsoleOptions{
		Message:       message,
		Options:       templateNames,
		OptionDetails: templateDetails,
		DefaultValue:  templateNames[0],
	})

	// separate this prompt from the next log
	console.Message(ctx, "")

	if err != nil {
		return nil, fmt.Errorf("prompting for template: %w", err)
	}

	if selected == 0 {
		return nil, nil
	}

	template := templates[selected-1]
	log.Printf("Selected template: %s", fmt.Sprint(template.RepositoryPath))

	return template, nil
}
