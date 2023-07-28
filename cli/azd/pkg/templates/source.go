package templates

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/resources"
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
	SourceFile     SourceKind = "file"
	SourceUrl      SourceKind = "url"
	SourceResource SourceKind = "resource"
)

type SourceConfig struct {
	Key      string     `json:"key,omitempty"`
	Name     string     `json:"name,omitempty"`
	Type     SourceKind `json:"type,omitempty"`
	Location string     `json:"location,omitempty"`
}

// Source returns a hydrated template source for the current config.
func (sc *SourceConfig) Source(ctx context.Context) (Source, error) {
	var source Source
	var err error

	switch sc.Type {
	case SourceFile:
		source, err = NewFileTemplateSource(sc.Name, sc.Location)
	case SourceUrl:
		source, err = NewUrlTemplateSource(ctx, sc.Name, sc.Location)
	case SourceResource:
		source, err = NewJsonTemplateSource(sc.Name, string(resources.TemplatesJson))
	default:
		err = fmt.Errorf("unknown template source type '%s'", sc.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("unable to create template source '%s': %w", sc.Key, err)
	}

	return source, nil
}
