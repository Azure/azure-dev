package environment

type ContextKeyNames string

const (
	AzdContextKey      ContextKeyNames = "azd"
	TemplateContextKey ContextKeyNames = "template"
	AzdCliContextKey   ContextKeyNames = "azdcli"
	HttpUtilContextKey ContextKeyNames = "httputil"
)
