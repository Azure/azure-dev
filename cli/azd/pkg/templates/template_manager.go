package templates

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/resources"
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
