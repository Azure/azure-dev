package templates

import (
	"context"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/resources"
)

type TemplateManager struct {
	sources []TemplateSource
}

// ListTemplates retrieves the list of templates in a deterministic order.
func (tm *TemplateManager) ListTemplates() ([]*Template, error) {
	allTemplates := []*Template{}

	for _, source := range tm.sources {
		templates, err := source.ListTemplates()
		if err != nil {
			return nil, fmt.Errorf("unable to list templates: %w", err)
		}

		allTemplates = append(allTemplates, templates...)
	}

	return allTemplates, nil
}

func (tm *TemplateManager) GetTemplate(name string) (*Template, error) {
	errors := []error{}

	for _, source := range tm.sources {
		template, err := source.GetTemplate(name)
		if err != nil {
			errors = append(errors, err)
			continue
		}

		return template, nil
	}

	return nil, fmt.Errorf("unable to find template '%s': %w", name, errors[0])
}

func NewTemplateManager() *TemplateManager {
	internalTemplateSource, _ := NewJsonTemplateSource(string(resources.TemplatesJson))

	return &TemplateManager{
		sources: []TemplateSource{internalTemplateSource},
	}
}

// PromptTemplate asks the user to select a template.
// An empty Template can be returned if the user selects the minimal template. This corresponds to the minimal azd template.
// See
func PromptTemplate(ctx context.Context, message string, console input.Console) (*Template, error) {
	templateManager := NewTemplateManager()
	templates, err := templateManager.ListTemplates()

	if err != nil {
		return nil, fmt.Errorf("prompting for template: %w", err)
	}

	choices := make([]string, 0, len(templates)+1)

	// prepend the minimal template option to guarantee first selection
	choices = append(choices, "Minimal")
	for _, template := range templates {
		choices = append(choices, template.Name)
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
