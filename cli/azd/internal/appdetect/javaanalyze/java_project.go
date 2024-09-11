package javaanalyze

type JavaProject struct {
	Services        []ServiceConfig  `json:"services"`
	Resources       []Resource       `json:"resources"`
	ServiceBindings []ServiceBinding `json:"serviceBindings"`
}

type Resource struct {
	Name            string           `json:"name"`
	Type            string           `json:"type"`
	BicepParameters []BicepParameter `json:"bicepParameters"`
	BicepProperties []BicepProperty  `json:"bicepProperties"`
}

type BicepParameter struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

type BicepProperty struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

type ResourceType int32

const (
	RESOURCE_TYPE_MYSQL         ResourceType = 0
	RESOURCE_TYPE_AZURE_STORAGE ResourceType = 1
)

// ServiceConfig represents a specific service's configuration.
type ServiceConfig struct {
	Name        string `json:"name"`
	ResourceURI string `json:"resourceUri"`
	Description string `json:"description"`
}

type ServiceBinding struct {
	Name        string   `json:"name"`
	ResourceURI string   `json:"resourceUri"`
	AuthType    AuthType `json:"authType"`
}

type AuthType int32

const (
	// Authentication type not specified.
	AuthType_SYSTEM_MANAGED_IDENTITY AuthType = 0
	// Username and Password Authentication.
	AuthType_USER_PASSWORD AuthType = 1
)
