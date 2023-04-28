package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/resources"
	"golang.org/x/exp/slices"
)

type TemplateManager struct {
}

// Get a set of templates where each key is the name of the template
func (tm *TemplateManager) ListTemplates() (map[string]Template, error) {
	result := make(map[string]Template)
	var templates []Template
	err := json.Unmarshal(resources.TemplatesJson, &templates)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal templates JSON %w", err)
	}

	for _, template := range templates {
		result[template.Name] = template
	}

	return result, nil
}

func (tm *TemplateManager) GetTemplate(templateName string) (Template, error) {
	templates, err := tm.ListTemplates()

	if err != nil {
		return Template{}, fmt.Errorf("unable to list templates: %w", err)
	}

	if matchingTemplate, ok := templates[templateName]; ok {
		return matchingTemplate, nil
	}

	return Template{}, fmt.Errorf("template with name '%s' was not found", templateName)
}

func NewTemplateManager() *TemplateManager {
	return &TemplateManager{}
}

// PromptTemplate ask the user to select a template.
// An empty Template with default values is returned if the user selects 'Empty Template' from the choices
func PromptTemplate(ctx context.Context, message string, console input.Console) (Template, error) {
	templateManager := NewTemplateManager()
	templatesSet, err := templateManager.ListTemplates()

	if err != nil {
		return Template{}, fmt.Errorf("prompting for template: %w", err)
	}

	// Create a map of PromptText->Template to be used by the prompt
	// This will be used to map the user selection to the template
	templateSelect := make(map[string]Template, len(templatesSet))
	for _, template := range templatesSet {
		// We trim the default prefix to make the template names more user-friendly
		// This is okay since templates without an organization name are assumed to be under "Azure-Samples/" by default
		name := strings.TrimPrefix(template.Name, "Azure-Samples/")
		if template.DisplayName != "" {
			// always show the proper name to the user
			templateSelect[template.DisplayName+" ("+name+")"] = template
		} else {
			templateSelect[name] = template
		}
	}

	choices := make([]string, 0, len(templateSelect)+1)
	for key := range templateSelect {
		choices = append(choices, key)
	}
	// sort based on the template name to provider stable ordering
	slices.SortFunc(choices, func(a, b string) bool {
		return templateSelect[a].Name < templateSelect[b].Name
	})

	// prepend the minimal option to guarantee first selection
	choices = append([]string{"Minimal"}, choices...)

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

	template := templateSelect[choices[selected]]
	log.Printf("Selected template: %s", fmt.Sprint(template.Name))

	return template, nil
}
