package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/resources"
)

type TemplateManager struct {
}

// ListTemplates retrieves the list of templates in a deterministic order.
func (tm *TemplateManager) ListTemplates() ([]Template, error) {
	var templates []Template
	err := json.Unmarshal(resources.TemplatesJson, &templates)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal templates JSON %w", err)
	}

	return templates, nil
}

func (tm *TemplateManager) GetTemplate(path string) (Template, error) {
	abs, err := Absolute(path)
	if err != nil {
		return Template{}, err
	}

	templates, err := tm.ListTemplates()

	if err != nil {
		return Template{}, fmt.Errorf("unable to list templates: %w", err)
	}

	for _, template := range templates {
		absPath, err := Absolute(template.RepositoryPath)
		if err != nil {
			panic(err)
		}

		if absPath == abs {
			return template, nil
		}
	}

	return Template{}, fmt.Errorf("template with name '%s' was not found", path)
}

func NewTemplateManager() *TemplateManager {
	return &TemplateManager{}
}

// PromptTemplate asks the user to select a template.
// An empty Template can be returned if the user selects the minimal template. This corresponds to the minimal azd template.
// See
func PromptTemplate(ctx context.Context, message string, console input.Bioc) (Template, error) {
	templateManager := NewTemplateManager()
	templates, err := templateManager.ListTemplates()

	if err != nil {
		return Template{}, fmt.Errorf("prompting for template: %w", err)
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
		return Template{}, fmt.Errorf("prompting for template: %w", err)
	}

	if selected == 0 {
		return Template{}, nil
	}

	template := templates[selected-1]
	log.Printf("Selected template: %s", fmt.Sprint(template.RepositoryPath))

	return template, nil
}
