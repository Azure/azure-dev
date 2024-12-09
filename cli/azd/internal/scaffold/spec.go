package scaffold

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
)

type InfraSpec struct {
	Parameters []Parameter
	Services   []ServiceSpec

	// Databases to create
	DbPostgres    *DatabasePostgres
	DbMySql       *DatabaseMySql
	DbRedis       *DatabaseRedis
	DbCosmosMongo *DatabaseCosmosMongo
	DbCosmos      *DatabaseCosmosAccount

	// ai models
	AIModels []AIModel

	AzureServiceBus     *AzureDepServiceBus
	AzureEventHubs      *AzureDepEventHubs
	AzureStorageAccount *AzureDepStorageAccount
}

type Parameter struct {
	Name   string
	Value  any
	Type   string
	Secret bool
}

type DatabasePostgres struct {
	DatabaseUser string
	DatabaseName string
	AuthType     internal.AuthType
}

type DatabaseMySql struct {
	DatabaseUser string
	DatabaseName string
	AuthType     internal.AuthType
}

type CosmosSqlDatabaseContainer struct {
	ContainerName     string
	PartitionKeyPaths []string
}

type DatabaseCosmosAccount struct {
	DatabaseName string
	Containers   []CosmosSqlDatabaseContainer
}

type DatabaseCosmosMongo struct {
	DatabaseName string
}

type DatabaseRedis struct {
}

// AIModel represents a deployed, ready to use AI model.
type AIModel struct {
	Name  string
	Model AIModelModel
}

// AIModelModel represents a model that backs the AIModel.
type AIModelModel struct {
	// The name of the underlying model.
	Name string
	// The version of the underlying model.
	Version string
}

type AzureDepServiceBus struct {
	Queues                 []string
	TopicsAndSubscriptions map[string][]string
	AuthType               internal.AuthType
	IsJms                  bool
}

type AzureDepEventHubs struct {
	EventHubNames     []string
	AuthType          internal.AuthType
	UseKafka          bool
	SpringBootVersion string
}

type AzureDepStorageAccount struct {
	ContainerNames []string
	AuthType       internal.AuthType
}

type ServiceSpec struct {
	Name string
	Port int

	Envs []Env

	// Front-end properties.
	Frontend *Frontend

	// Back-end properties
	Backend *Backend

	// Connection to a database
	DbPostgres    *DatabasePostgres
	DbMySql       *DatabaseMySql
	DbRedis       *DatabaseRedis
	DbCosmosMongo *DatabaseCosmosMongo
	DbCosmos      *DatabaseCosmosAccount

	// AI model connections
	AIModels []AIModelReference

	AzureServiceBus     *AzureDepServiceBus
	AzureEventHubs      *AzureDepEventHubs
	AzureStorageAccount *AzureDepStorageAccount
}

type Env struct {
	Name  string
	Value string
}

type Frontend struct {
	Backends []ServiceReference
}

type Backend struct {
	Frontends []ServiceReference
}

type ServiceReference struct {
	Name string
}

type AIModelReference struct {
	Name string
}

func containerAppExistsParameter(serviceName string) Parameter {
	return Parameter{
		Name: BicepName(serviceName) + "Exists",
		Value: fmt.Sprintf("${SERVICE_%s_RESOURCE_EXISTS=false}",
			strings.ReplaceAll(strings.ToUpper(serviceName), "-", "_")),
		Type: "bool",
	}
}

type serviceDef struct {
	Settings []serviceDefSettings `json:"settings"`
}

type serviceDefSettings struct {
	Name         string `json:"name"`
	Value        string `json:"value"`
	Secret       bool   `json:"secret,omitempty"`
	SecretRef    string `json:"secretRef,omitempty"`
	CommentName  string `json:"_comment_name,omitempty"`
	CommentValue string `json:"_comment_value,omitempty"`
}

func serviceDefPlaceholder(serviceName string) Parameter {
	return Parameter{
		Name: BicepName(serviceName) + "Definition",
		Value: serviceDef{
			Settings: []serviceDefSettings{
				{
					Name:        "",
					Value:       "${VAR}",
					CommentName: "The name of the environment variable when running in Azure. If empty, ignored.",
					//nolint:lll
					CommentValue: "The value to provide. This can be a fixed literal, or an expression like ${VAR} to use the value of 'VAR' from the current environment.",
				},
				{
					Name:        "",
					Value:       "${VAR_S}",
					Secret:      true,
					CommentName: "The name of the environment variable when running in Azure. If empty, ignored.",
					//nolint:lll
					CommentValue: "The value to provide. This can be a fixed literal, or an expression like ${VAR_S} to use the value of 'VAR_S' from the current environment.",
				},
			},
		},
		Type:   "object",
		Secret: true,
	}
}

func AddNewEnvironmentVariable(serviceSpec *ServiceSpec, name string, value string) error {
	merged, err := mergeEnvWithDuplicationCheck(serviceSpec.Envs,
		[]Env{
			{
				Name:  name,
				Value: value,
			},
		},
	)
	if err != nil {
		return err
	}
	serviceSpec.Envs = merged
	return nil
}
