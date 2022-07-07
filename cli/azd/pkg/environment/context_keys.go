package environment

type ContextKeyNames string

const (
	AzdContextKey      ContextKeyNames = "azd"
	OptionsContextKey  ContextKeyNames = "options"
	TemplateContextKey ContextKeyNames = "template"
	AzdCliContextKey   ContextKeyNames = "azdcli"
	HttpUtilContextKey ContextKeyNames = "httputil"
)
