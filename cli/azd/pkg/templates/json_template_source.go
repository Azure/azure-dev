package templates

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/exp/slices"
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
	return jts.templates, nil
}

func (jts *jsonTemplateSource) GetTemplate(ctx context.Context, name string) (*Template, error) {
	index := slices.IndexFunc(jts.templates, func(t *Template) bool {
		return t.Name == name
	})

	if index < 0 {
		return nil, fmt.Errorf("template with name '%s' was not found", name)
	}

	return jts.templates[index], nil
}
