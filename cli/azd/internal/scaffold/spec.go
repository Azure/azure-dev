// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"fmt"
	"strings"
)

type InfraSpec struct {
	Parameters []Parameter
	Services   []ServiceSpec

	// Databases to create
	DbPostgres    *DatabasePostgres
	DbMySql       *DatabaseMysql
	DbCosmosMongo *DatabaseCosmosMongo
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

	Env map[string]string

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
	DbRedis       *DatabaseReference

	StorageAccount *StorageReference

	// AI model connections
	AIModels []AIModelReference

	// Messaging services
	ServiceBus *ServiceBus
	EventHubs  *EventHubs

	HasAiFoundryProject *AiFoundrySpec
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

type KeyVaultReference struct {
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
