package devcentersdk

import (
	"errors"
	"regexp"
	"time"
)

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

type ResourceGroupId struct {
	Id             string
	SubscriptionId string
	Name           string
}

//nolint:lll
var (
	resourceIdRegex = regexp.MustCompile(
		`\/subscriptions\/(?P<subscriptionId>.+?)\/resourceGroups\/(?P<resourceGroup>.+?)\/providers\/(?P<resourceProvider>.+?)\/(?P<resourcePath>.+?)\/(?P<resourceName>.+)`,
	)
	resourceGroupIdRegex = regexp.MustCompile(
		`\/subscriptions\/(?P<subscriptionId>.+?)\/resourceGroups\/(?P<resourceGroup>.+)`,
	)
)

func NewResourceId(resourceId string) (*ResourceId, error) {
	// Find matches and extract named values
	matches := resourceIdRegex.FindStringSubmatch(resourceId)

	if len(matches) == 0 {
		return nil, errors.New("no match found")
	}

	namedValues := getRegExpNamedValues(resourceIdRegex, matches)

	return &ResourceId{
		Id:             resourceId,
		SubscriptionId: namedValues["subscriptionId"],
		ResourceGroup:  namedValues["resourceGroup"],
		Provider:       namedValues["resourceProvider"],
		ResourcePath:   namedValues["resourcePath"],
		ResourceName:   namedValues["resourceName"],
	}, nil
}

func NewResourceGroupId(resourceId string) (*ResourceGroupId, error) {
	// Find matches and extract named values
	matches := resourceGroupIdRegex.FindStringSubmatch(resourceId)

	if len(matches) == 0 {
		return nil, errors.New("no match found")
	}

	namedValues := getRegExpNamedValues(resourceGroupIdRegex, matches)

	return &ResourceGroupId{
		Id:             resourceId,
		SubscriptionId: namedValues["subscriptionId"],
		Name:           namedValues["resourceGroup"],
	}, nil
}

func getRegExpNamedValues(regexp *regexp.Regexp, matches []string) map[string]string {
	namedValues := make(map[string]string)

	// The first element in the match slice is the entire matched string,
	// so we start the loop from 1 to skip it.
	for i, name := range regexp.SubexpNames()[1:] {
		namedValues[name] = matches[i+1]
	}

	return namedValues
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
	Name                      string            `json:"name"`
	EnvironmentType           string            `json:"environmentType"`
	User                      string            `json:"user"`
	ProvisioningState         ProvisioningState `json:"provisioningState"`
	ResourceGroupId           string            `json:"resourceGroupId"`
	CatalogName               string            `json:"catalogName"`
	EnvironmentDefinitionName string            `json:"environmentDefinitionName"`
	Parameters                map[string]any    `json:"parameters"`
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

type OutputListResponse struct {
	Outputs map[string]OutputParameter `json:"outputs"`
}

type OutputParameter struct {
	Type      OutputParameterType `json:"type"`
	Value     any                 `json:"value"`
	Sensitive bool                `json:"sensitive"`
}

type OutputParameterType string

const (
	OutputParameterTypeArray   OutputParameterType = "array"
	OutputParameterTypeBoolean OutputParameterType = "boolean"
	OutputParameterTypeNumber  OutputParameterType = "number"
	OutputParameterTypeObject  OutputParameterType = "object"
	OutputParameterTypeString  OutputParameterType = "string"
)
