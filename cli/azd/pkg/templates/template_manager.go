package templates

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/resources"
)

type TemplateManager struct {
}

func (tm *TemplateManager) ListTemplates() ([]Template, error) {
	var templates []Template
	err := json.Unmarshal(resources.TemplatesJson, &templates)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal templates JSON %w", err)
	}

	return templates, nil
}

func (tm *TemplateManager) GetTemplate(templateName string) (Template, error) {
	templates, err := tm.ListTemplates()

	if err != nil {
		return Template{}, fmt.Errorf("unable to list templates: %w", err)
	}

	var matchingTemplate *Template

	for _, template := range templates {
		if template.Name == templateName {
			matchingTemplate = &template
			break
		}
	}

	if matchingTemplate == nil {
		return Template{}, fmt.Errorf("template with name '%s' was not found", templateName)
	}

	return *matchingTemplate, nil
}

func NewTemplateManager() *TemplateManager {
	return &TemplateManager{}
}
