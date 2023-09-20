package templates

import (
	"context"
	"fmt"
)

// Source is a source of AZD compatible templates.
type Source interface {
	// Name returns the name of the source.
	Name() string
	// ListTemplates returns a list of AZD compatible templates.
	ListTemplates(ctx context.Context) ([]*Template, error)
	// GetTemplate returns a template by path.
	GetTemplate(ctx context.Context, path string) (*Template, error)
}

type SourceKind string

const (
	SourceKindFile       SourceKind = "file"
	SourceKindUrl        SourceKind = "url"
	SourceKindResource   SourceKind = "resource"
	SourceKindAwesomeAzd SourceKind = "awesome-azd"
	SourceKindDevCenter  SourceKind = "devcenter"
)

type SourceConfig struct {
	Key      string     `json:"key,omitempty"`
	Name     string     `json:"name,omitempty"`
	Type     SourceKind `json:"type,omitempty"`
	Location string     `json:"location,omitempty"`
}

type templateSource struct {
	name      string
	templates []*Template
}

// NewJsonTemplateSource creates a new template source from a JSON string.
func NewTemplateSource(name string, templates []*Template) (Source, error) {
	return &templateSource{
		name:      name,
		templates: templates,
	}, nil
}

func (ts *templateSource) Name() string {
	return ts.name
}

func (ts *templateSource) ListTemplates(ctx context.Context) ([]*Template, error) {
	for _, template := range ts.templates {
		template.Source = ts.name
	}

	return ts.templates, nil
}

func (ts *templateSource) GetTemplate(ctx context.Context, path string) (*Template, error) {
	templates, err := ts.ListTemplates(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to list templates: %w", err)
	}

	for _, template := range templates {
		if template.RepositoryPath == path {
			return template, nil
		}
	}

	return nil, fmt.Errorf("template with path '%s' was not found, %w", path, ErrTemplateNotFound)
}
