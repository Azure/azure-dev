package devcentersdk

import "time"

type DevCenter struct {
	Id             string
	SubscriptionId string
	ResourceGroup  string
	Name           string
	ServiceUri     string
}

type DevCenterListResponse struct {
	Value []*DevCenter `json:"value"`
}

type Project struct {
	Id             string
	SubscriptionId string
	ResourceGroup  string
	Name           string
	Description    string
	DevCenter      *DevCenter
}

type GenericResource struct {
	Id         string                 `json:"id"`
	Location   string                 `json:"location"`
	TenantId   string                 `json:"tenantId"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
}

type ResourceId struct {
	Id             string
	SubscriptionId string
	ResourceGroup  string
	Provider       string
	ResourcePath   string
	ResourceName   string
}

type ProjectListResponse struct {
	Value []*Project `json:"value"`
}

type Catalog struct {
	Name string `json:"name"`
}

type CatalogListResponse struct {
	Value []*Catalog `json:"value"`
}

type EnvironmentType struct {
	Name               string `json:"name"`
	DeploymentTargetId string `json:"deploymentTargetId"`
	Status             string `json:"status"`
}

type EnvironmentTypeListResponse struct {
	Value []*EnvironmentType `json:"value"`
}

type EnvironmentDefinition struct {
	Id           string      `json:"id"`
	Name         string      `json:"name"`
	CatalogName  string      `json:"catalogName"`
	Description  string      `json:"description"`
	TemplatePath string      `json:"templatePath"`
	Parameters   []Parameter `json:"parameters"`
}

type EnvironmentDefinitionListResponse struct {
	Value []*EnvironmentDefinition `json:"value"`
}

type ParameterType string

const (
	ParameterTypeString ParameterType = "string"
	ParameterTypeInt    ParameterType = "int"
	ParameterTypeBool   ParameterType = "bool"
)

type Parameter struct {
	Id          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Type        ParameterType `json:"type"`
	ReadOnly    bool          `json:"readOnly"`
	Required    bool          `json:"required"`
	Allowed     []string      `json:"allowed"`
	Default     any           `json:"default"`
}

type ProvisioningState string

const (
	ProvisioningStateSucceeded ProvisioningState = "Succeeded"
	ProvisioningStateCreating  ProvisioningState = "Creating"
	ProvisioningStateDeleting  ProvisioningState = "Deleting"
)

type Environment struct {
	Name                      string
	EnvironmentType           string
	User                      string
	ProvisioningState         ProvisioningState
	ResourceGroupId           string
	CatalogName               string
	EnvironmentDefinitionName string
	Parameters                map[string]any
}

type EnvironmentListResponse struct {
	Value []*Environment `json:"value"`
}

type EnvironmentSpec struct {
	CatalogName               string         `json:"catalogName"`
	EnvironmentDefinitionName string         `json:"environmentDefinitionName"`
	EnvironmentType           string         `json:"environmentType"`
	Parameters                map[string]any `json:"parameters"`
}

type EnvironmentPutResponse struct {
	*Environment
}

type OperationStatus struct {
	Id        string    `json:"id"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
}
