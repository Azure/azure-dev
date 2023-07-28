package templates

import (
	"context"
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
