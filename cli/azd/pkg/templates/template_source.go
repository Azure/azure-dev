package templates

// TemplateSource is a source of AZD compatible templates.
type TemplateSource interface {
	// ListTemplates returns a list of AZD compatible templates.
	ListTemplates() ([]*Template, error)
	// GetTemplate returns a template by name.
	GetTemplate(name string) (*Template, error)
}
