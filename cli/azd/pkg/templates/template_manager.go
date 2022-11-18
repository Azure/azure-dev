package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/resources"
	"golang.org/x/exp/maps"
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
	var result Template
	templateManager := NewTemplateManager()
	templatesSet, err := templateManager.ListTemplates()

	if err != nil {
		return result, fmt.Errorf("prompting for template: %w", err)
	}

	templateNames := []string{"Empty Template"}
	names := maps.Keys(templatesSet)
	sort.Strings(names)
	templateNames = append(templateNames, names...)

	selectedIndex, err := console.Select(ctx, input.ConsoleOptions{
		Message:      message,
		Options:      templateNames,
		DefaultValue: templateNames[0],
	})

	// separate this prompt from the next log
	console.Message(ctx, "")

	if err != nil {
		return result, fmt.Errorf("prompting for template: %w", err)
	}

	if selectedIndex == 0 {
		return result, nil
	}

	selectedTemplateName := templateNames[selectedIndex]
	log.Printf("Selected template: %s", fmt.Sprint(selectedTemplateName))

	return templatesSet[selectedTemplateName], nil
}
