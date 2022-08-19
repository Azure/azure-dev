package environment

type ContextKeyNames string

const (
	TemplateContextKey ContextKeyNames = "template"
	AzdCliContextKey   ContextKeyNames = "azdcli"
	HttpUtilContextKey ContextKeyNames = "httputil"
)
