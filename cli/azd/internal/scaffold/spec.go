package scaffold

import (
	"fmt"
	"github.com/azure/azure-dev/cli/azd/internal"
	"strings"
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

var resourceConnectionEnvPrefix = "$resource.connection"

func isResourceConnectionEnv(env string) bool {
	if !strings.HasPrefix(env, resourceConnectionEnvPrefix) {
		return false
	}
	a := strings.Split(env, ":")
	if len(a) != 3 {
		return false
	}
	return a[0] != "" && a[1] != "" && a[2] != ""
}

func ToResourceConnectionEnv(resourceType ResourceType, resourceInfoType ResourceInfoType) string {
	return fmt.Sprintf("%s:%s:%s", resourceConnectionEnvPrefix, resourceType, resourceInfoType)
}

func toResourceConnectionInfo(resourceConnectionEnv string) (resourceType ResourceType,
	resourceInfoType ResourceInfoType) {
	if !isResourceConnectionEnv(resourceConnectionEnv) {
		return "", ""
	}
	a := strings.Split(resourceConnectionEnv, ":")
	return ResourceType(a[1]), ResourceInfoType(a[2])
}

// todo merge ResourceType and project.ResourceType
// Not use project.ResourceType because it will cause cycle import.
// Not merge it in current PR to avoid conflict with upstream main branch.
// Solution proposal: define a ResourceType in lower level that can be used both in scaffold and project package.

type ResourceType string

const (
	ResourceTypeDbRedis             ResourceType = "db.redis"
	ResourceTypeDbPostgres          ResourceType = "db.postgres"
	ResourceTypeDbMySQL             ResourceType = "db.mysql"
	ResourceTypeDbMongo             ResourceType = "db.mongo"
	ResourceTypeDbCosmos            ResourceType = "db.cosmos"
	ResourceTypeHostContainerApp    ResourceType = "host.containerapp"
	ResourceTypeOpenAiModel         ResourceType = "ai.openai.model"
	ResourceTypeMessagingServiceBus ResourceType = "messaging.servicebus"
	ResourceTypeMessagingEventHubs  ResourceType = "messaging.eventhubs"
	ResourceTypeMessagingKafka      ResourceType = "messaging.kafka"
	ResourceTypeStorage             ResourceType = "storage"
)

type ResourceInfoType string

const (
	ResourceInfoTypeHost             ResourceInfoType = "host"
	ResourceInfoTypePort             ResourceInfoType = "port"
	ResourceInfoTypeEndpoint         ResourceInfoType = "endpoint"
	ResourceInfoTypeDatabaseName     ResourceInfoType = "databaseName"
	ResourceInfoTypeNamespace        ResourceInfoType = "namespace"
	ResourceInfoTypeAccountName      ResourceInfoType = "accountName"
	ResourceInfoTypeUsername         ResourceInfoType = "username"
	ResourceInfoTypePassword         ResourceInfoType = "password"
	ResourceInfoTypeUrl              ResourceInfoType = "url"
	ResourceInfoTypeJdbcUrl          ResourceInfoType = "jdbcUrl"
	ResourceInfoTypeConnectionString ResourceInfoType = "connectionString"
)

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
