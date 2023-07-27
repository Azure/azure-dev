package templates

import "context"

// Source is a source of AZD compatible templates.
type Source interface {
	// Name returns the name of the source.
	Name() string
	// ListTemplates returns a list of AZD compatible templates.
	ListTemplates(ctx context.Context) ([]*Template, error)
	// GetTemplate returns a template by name.
	GetTemplate(ctx context.Context, name string) (*Template, error)
}

type SourceKind string

const (
	SourceFile     SourceKind = "file"
	SourceUrl      SourceKind = "url"
	SourceResource SourceKind = "resource"
)

type SourceConfig struct {
	Name     string     `json:"name"`
	Type     SourceKind `json:"type"`
	Location string     `json:"location"`
}
