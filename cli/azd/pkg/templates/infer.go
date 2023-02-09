package templates

type ApplicationType string

const (
	// A front-end SPA / static web app.
	WebApp ApplicationType = "web"
	// API only.
	ApiApp ApplicationType = "api"
	// Fullstack solution. Front-end SPA with back-end API.
	ApiWeb ApplicationType = "api-web"
)

type Characteristics struct {
	Type ApplicationType

	LanguageTags       []string
	InfrastructureTags []string

	// Capabilities specified in key OPERATOR value
	// runtime>1.10
	Capabilities []string
}

func (c *Characteristics) Description() string {
	return ""
}

// Attempts to match to an official template.
func MatchToOfficial(c Characteristics) *Template {
	if c.Type != "" {
		return &Template{RepositoryPath: string(c.Type)}
	}

	return nil
}
