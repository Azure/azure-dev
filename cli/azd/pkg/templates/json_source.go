package templates

import (
	"context"
	"encoding/json"
	"fmt"
)

type jsonTemplateSource struct {
	name      string
	templates []*Template
}

// NewJsonTemplateSource creates a new template source from a JSON string.
func NewJsonTemplateSource(name string, jsonTemplates string) (Source, error) {
	var templates []*Template
	err := json.Unmarshal([]byte(jsonTemplates), &templates)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal templates JSON %w", err)
	}

	return &jsonTemplateSource{
		name:      name,
		templates: templates,
	}, nil
}

func (jts *jsonTemplateSource) Name() string {
	return jts.name
}

func (jts *jsonTemplateSource) ListTemplates(ctx context.Context) ([]*Template, error) {
	for _, template := range jts.templates {
		template.Source = jts.name
	}

	return jts.templates, nil
}

func (jts *jsonTemplateSource) GetTemplate(ctx context.Context, path string) (*Template, error) {
	templates, err := jts.ListTemplates(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to list templates: %w", err)
	}

	for _, template := range templates {
		if template.RepositoryPath == path {
			return template, nil
		}
	}

	return nil, fmt.Errorf("template with name '%s' was not found, %w", path, ErrTemplateNotFound)
}
