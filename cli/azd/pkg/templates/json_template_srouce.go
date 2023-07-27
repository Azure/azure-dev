package templates

import (
	"encoding/json"
	"fmt"

	"golang.org/x/exp/slices"
)

type jsonTemplateSource struct {
	templates []*Template
}

// NewJsonTemplateSource creates a new template source from a JSON string.
func NewJsonTemplateSource(jsonTemplates string) (TemplateSource, error) {
	var templates []*Template
	err := json.Unmarshal([]byte(jsonTemplates), &templates)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal templates JSON %w", err)
	}

	return &jsonTemplateSource{
		templates: templates,
	}, nil
}

func (jts *jsonTemplateSource) ListTemplates() ([]*Template, error) {
	return jts.templates, nil
}

func (jts *jsonTemplateSource) GetTemplate(name string) (*Template, error) {
	index := slices.IndexFunc(jts.templates, func(t *Template) bool {
		return t.Name == name
	})

	if index < 0 {
		return nil, fmt.Errorf("template with name '%s' was not found", name)
	}

	return jts.templates[index], nil
}
