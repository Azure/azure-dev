// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"fmt"
	"strings"
)

type InfraSpec struct {
	Parameters []Parameter

	Services []ServiceSpec

	// Existing resources for declaration purposes.
	// These are resources that are already created and should be used by the
	// current deployment for referencing
	Existing []ExistingResource

	// Databases to create
	DbPostgres    *DatabasePostgres
	DbMySql       *DatabaseMysql
	DbCosmosMongo *DatabaseCosmosMongo
	DbCosmos      *DatabaseCosmos
	DbRedis       *DatabaseRedis

	// Key vault
	KeyVault *KeyVault

	// Messaging services
	ServiceBus *ServiceBus
	EventHubs  *EventHubs

	// Storage account
	StorageAccount *StorageAccount

	// ai models
	AIModels []AIModel

	// ai foundry models
	AiFoundryProject *AiFoundrySpec

	AISearch *AISearch
}

type Parameter struct {
	Name   string
	Value  any
	Type   string
	Secret bool
}

type DatabasePostgres struct {
	DatabaseName string
}

type DatabaseMysql struct {
	DatabaseName string
}

type DatabaseCosmosMongo struct {
	DatabaseName string
}

type DatabaseCosmos struct {
	DatabaseName string
	Containers   []CosmosSqlDatabaseContainer
}

type CosmosSqlDatabaseContainer struct {
	ContainerName     string
	PartitionKeyPaths []string
}

type DatabaseRedis struct {
}

// AIModel represents a deployed, ready to use AI model.
type AIModel struct {
	Name  string
	Model AIModelModel
}

// AIModel represents a deployed, ready to use AI model.
type AiFoundrySpec struct {
	Name   string
	Models []AiFoundryModel
}

type AiFoundryModel struct {
	AIModelModel
	Format string            `yaml:"format,omitempty"`
	Sku    AiFoundryModelSku `yaml:"sku,omitempty"`
}

type AiFoundryModelSku struct {
	Name      string `yaml:"name,omitempty"`
	UsageName string `yaml:"usageName,omitempty"`
	Capacity  int32  `yaml:"capacity,omitempty"`
}

// AIModelModel represents a model that backs the AIModel.
type AIModelModel struct {
	// The name of the underlying model.
	Name string
	// The version of the underlying model.
	Version string
}

type AISearch struct {
}

type ServiceBus struct {
	Queues []string
	Topics []string
}

type EventHubs struct {
	Hubs []string
}

type KeyVault struct {
}

type StorageAccount struct {
	Containers []string
}

type ServiceSpec struct {
	Name string
	Port int
	Host HostKind

	Env map[string]string

	// App Service specific configuration
	Runtime        *RuntimeInfo
	StartupCommand string

	// Front-end properties.
	Frontend *Frontend

	// Back-end properties
	Backend *Backend

	// Key vault
	KeyVault *KeyVaultReference

	// Connection to a database
	DbPostgres    *DatabaseReference
	DbMySql       *DatabaseReference
	DbCosmosMongo *DatabaseReference
	DbCosmos      *DatabaseReference
	DbRedis       *DatabaseReference

	StorageAccount *StorageReference

	// AI model connections
	AIModels []AIModelReference

	// Messaging services
	ServiceBus *ServiceBus
	EventHubs  *EventHubs

	AiFoundryProject *AiFoundrySpec

	AISearch *AISearchReference

	// Existing resource bindings
	Existing []*ExistingResource
}

type HostKind string

const (
	AppServiceKind   HostKind = "appservice"
	ContainerAppKind HostKind = "containerapp"
	StaticWebAppKind HostKind = "staticwebapp"
)

type RuntimeInfo struct {
	Type    string
	Version string
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

type DatabaseReference struct {
	DatabaseName string
}

type AIModelReference struct {
	Name string
}

type StorageReference struct {
}

type AISearchReference struct {
}

type KeyVaultReference struct {
}

type ExistingResource struct {
	// The unique logical name of the existing resource in the infra scope.
	Name string
	// The resource ID of the resource.
	ResourceIdEnvVar string
	// The resource type of the resource. This should match the type contained in the resource ID.
	ResourceType string
	// The API version of the resource to look up values.
	ApiVersion string
	// Role assignment
	RoleAssignments []RoleAssignment
}

func containerAppExistsParameter(serviceName string) Parameter {
	return Parameter{
		Name: BicepName(serviceName) + "Exists",
		Value: fmt.Sprintf("${SERVICE_%s_RESOURCE_EXISTS=false}",
			strings.ReplaceAll(strings.ToUpper(serviceName), "-", "_")),
		Type: "bool",
	}
}
