package templates

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"golang.org/x/exp/slices"
)

type TemplateManager struct {
	sourceManager SourceManager
	sources       []Source
}

func NewTemplateManager(sourceManager SourceManager) (*TemplateManager, error) {
	return &TemplateManager{
		sourceManager: sourceManager,
	}, nil
}

type ListOptions struct {
	Source string
}

type sourceFilterPredicate func(config *SourceConfig) bool

// ListTemplates retrieves the list of templates in a deterministic order.
func (tm *TemplateManager) ListTemplates(ctx context.Context, options *ListOptions) ([]*Template, error) {
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

		slices.SortFunc(templates, func(a *Template, b *Template) bool {
			return a.RepositoryPath < b.RepositoryPath
		})

		allTemplates = append(allTemplates, templates...)
	}

	return allTemplates, nil
}

func (tm *TemplateManager) GetTemplate(ctx context.Context, name string) (*Template, error) {
	errors := []error{}

	sources, err := tm.getSources(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed listing templates: %w", err)
	}

	for _, source := range sources {
		template, err := source.GetTemplate(ctx, name)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		return template, nil
	}

	return nil, fmt.Errorf("unable to find template '%s': %w", name, errors[0])
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

		source, err := config.Source(ctx)
		if err != nil {
			return nil, err
		}

		sources = append(sources, source)
	}

	return sources, nil
}

// PromptTemplate asks the user to select a template.
// An empty Template can be returned if the user selects the minimal template. This corresponds to the minimal azd template.
// See
func PromptTemplate(ctx context.Context, message string, console input.Console) (*Template, error) {
	templateManager, err := NewTemplateManager(NewSourceManager(config.NewUserConfigManager()))
	if err != nil {
		return nil, fmt.Errorf("prompting for template: %w", err)
	}

	templates, err := templateManager.ListTemplates(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("prompting for template: %w", err)
	}

	choices := make([]string, 0, len(templates)+1)

	// prepend the minimal template option to guarantee first selection
	choices = append(choices, "Minimal")
	for _, template := range templates {
		choices = append(choices, fmt.Sprintf("%s (%s)", template.Name, template.RepositoryPath))
	}

	selected, err := console.Select(ctx, input.ConsoleOptions{
		Message:      message,
		Options:      choices,
		DefaultValue: choices[0],
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
