// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"

	"github.com/braydonk/yaml"
)

type ResourceType string

func AllResourceTypes() []ResourceType {
	return []ResourceType{
		ResourceTypeDbRedis,
		ResourceTypeDbPostgres,
		ResourceTypeDbMySql,
		ResourceTypeDbMongo,
		ResourceTypeDbCosmos,
		ResourceTypeHostContainerApp,
		ResourceTypeOpenAiModel,
		ResourceTypeMessagingEventHubs,
		ResourceTypeMessagingServiceBus,
		ResourceTypeStorage,
		ResourceTypeAiProject,
		ResourceTypeAiSearch,
		ResourceTypeKeyVault,
	}
}

const (
	ResourceTypeDbRedis             ResourceType = "db.redis"
	ResourceTypeDbPostgres          ResourceType = "db.postgres"
	ResourceTypeDbMySql             ResourceType = "db.mysql"
	ResourceTypeDbMongo             ResourceType = "db.mongo"
	ResourceTypeDbCosmos            ResourceType = "db.cosmos"
	ResourceTypeHostContainerApp    ResourceType = "host.containerapp"
	ResourceTypeOpenAiModel         ResourceType = "ai.openai.model"
	ResourceTypeMessagingEventHubs  ResourceType = "messaging.eventhubs"
	ResourceTypeMessagingServiceBus ResourceType = "messaging.servicebus"
	ResourceTypeStorage             ResourceType = "storage"
	ResourceTypeAiProject           ResourceType = "ai.project"
	ResourceTypeAiSearch            ResourceType = "ai.search"
	ResourceTypeKeyVault            ResourceType = "keyvault"
)

func (r ResourceType) String() string {
	switch r {
	case ResourceTypeDbRedis:
		return "Redis"
	case ResourceTypeDbPostgres:
		return "PostgreSQL"
	case ResourceTypeDbMySql:
		return "MySQL"
	case ResourceTypeDbMongo:
		return "MongoDB"
	case ResourceTypeDbCosmos:
		return "CosmosDB"
	case ResourceTypeHostContainerApp:
		return "Container App"
	case ResourceTypeOpenAiModel:
		return "Open AI Model"
	case ResourceTypeMessagingEventHubs:
		return "Event Hubs"
	case ResourceTypeMessagingServiceBus:
		return "Service Bus"
	case ResourceTypeStorage:
		return "Storage Account"
	case ResourceTypeAiProject:
		return "AI Foundry"
	case ResourceTypeAiSearch:
		return "AI Search"
	case ResourceTypeKeyVault:
		return "Key Vault"
	}

	return ""
}

func (r ResourceType) AzureResourceType() string {
	// DEV note:
	// This can be updated after the resource is fully generated in scaffold
	// and well-tested.
	//
	// Alongside this, the resource type should be updated in the scaffold/resource_meta.go
	// See notes there on how to easily obtain the resource type for new AVM modules.
	switch r {
	case ResourceTypeHostContainerApp:
		return "Microsoft.App/containerApps"
	case ResourceTypeDbRedis:
		return "Microsoft.Cache/redis"
	case ResourceTypeDbPostgres:
		return "Microsoft.DBforPostgreSQL/flexibleServers/databases"
	case ResourceTypeDbMySql:
		return "Microsoft.DBforMySQL/flexibleServers/databases"
	case ResourceTypeDbMongo:
		return "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases"
	case ResourceTypeOpenAiModel:
		return "Microsoft.CognitiveServices/accounts/deployments"
	case ResourceTypeDbCosmos:
		return "Microsoft.DocumentDB/databaseAccounts/sqlDatabases"
	case ResourceTypeMessagingEventHubs:
		return "Microsoft.EventHub/namespaces"
	case ResourceTypeMessagingServiceBus:
		return "Microsoft.ServiceBus/namespaces"
	case ResourceTypeStorage:
		return "Microsoft.Storage/storageAccounts"
	case ResourceTypeKeyVault:
		return "Microsoft.KeyVault/vaults"
	case ResourceTypeAiProject:
		return "Microsoft.MachineLearningServices/workspaces"
	case ResourceTypeAiSearch:
		return "Microsoft.Search/searchServices"
	}

	return ""
}

type ResourceConfig struct {
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"-"`
	// Type of resource
	Type ResourceType `yaml:"type"`
	// The name of the resource
	Name string `yaml:"name,omitempty"`
	// The properties for the resource
	RawProps map[string]yaml.Node `yaml:",inline"`
	Props    interface{}          `yaml:"-"`
	// Relationships to other resources
	Uses []string `yaml:"uses,omitempty"`

	// IncludeName indicates whether the `name` field should be included upon serialization.
	IncludeName bool `yaml:"-"`
}

func (r *ResourceConfig) MarshalYAML() (interface{}, error) {
	type rawResourceConfig ResourceConfig
	raw := rawResourceConfig(*r)

	if !raw.IncludeName {
		raw.Name = ""
	}

	var marshalRawProps = func(in interface{}) error {
		marshaled, err := yaml.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshaling props: %w", err)
		}

		props := map[string]yaml.Node{}
		if err := yaml.Unmarshal(marshaled, &props); err != nil {
			return err
		}
		raw.RawProps = props
		return nil
	}

	var errMarshal error
	switch raw.Type {
	case ResourceTypeOpenAiModel:
		errMarshal = marshalRawProps(raw.Props.(AIModelProps))
	case ResourceTypeHostContainerApp:
		errMarshal = marshalRawProps(raw.Props.(ContainerAppProps))
	case ResourceTypeDbCosmos:
		errMarshal = marshalRawProps(raw.Props.(CosmosDBProps))
	case ResourceTypeMessagingEventHubs:
		errMarshal = marshalRawProps(raw.Props.(EventHubsProps))
	case ResourceTypeMessagingServiceBus:
		errMarshal = marshalRawProps(raw.Props.(ServiceBusProps))
	case ResourceTypeStorage:
		errMarshal = marshalRawProps(raw.Props.(StorageProps))
	case ResourceTypeAiProject:
		errMarshal = marshalRawProps(raw.Props.(AiFoundryModelProps))
	}

	if errMarshal != nil {
		return nil, errMarshal
	}

	return raw, nil
}

func (r *ResourceConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawResourceConfig ResourceConfig
	raw := rawResourceConfig{}
	if err := value.Decode(&raw); err != nil {
		return err
	}

	var unmarshalProps = func(v interface{}) error {
		value, err := yaml.Marshal(raw.RawProps)
		if err != nil {
			return fmt.Errorf("failed to marshal raw props: %w", err)
		}

		if err := yaml.Unmarshal(value, v); err != nil {
			return err
		}

		return nil
	}

	// Unmarshal props based on type
	switch raw.Type {
	case ResourceTypeOpenAiModel:
		amp := AIModelProps{}
		if err := unmarshalProps(&amp); err != nil {
			return err
		}
		raw.Props = amp
	case ResourceTypeHostContainerApp:
		cap := ContainerAppProps{}
		if err := unmarshalProps(&cap); err != nil {
			return err
		}
		raw.Props = cap
	case ResourceTypeDbCosmos:
		cdp := CosmosDBProps{}
		if err := unmarshalProps(&cdp); err != nil {
			return err
		}
		raw.Props = cdp
	case ResourceTypeMessagingEventHubs:
		ehp := EventHubsProps{}
		if err := unmarshalProps(&ehp); err != nil {
			return err
		}
		raw.Props = ehp
	case ResourceTypeMessagingServiceBus:
		sbp := ServiceBusProps{}
		if err := unmarshalProps(&sbp); err != nil {
			return err
		}
		raw.Props = sbp
	case ResourceTypeStorage:
		sp := StorageProps{}
		if err := unmarshalProps(&sp); err != nil {
			return err
		}
		raw.Props = sp
	case ResourceTypeAiProject:
		amp := AiFoundryModelProps{}
		if err := unmarshalProps(&amp); err != nil {
			return err
		}
		raw.Props = amp
	}

	*r = ResourceConfig(raw)
	return nil
}

type ContainerAppProps struct {
	Port int             `yaml:"port,omitempty"`
	Env  []ServiceEnvVar `yaml:"env,omitempty"`
}

type ServiceEnvVar struct {
	Name string `yaml:"name,omitempty"`

	// either Value or Secret can be set, but not both
	Value  string `yaml:"value,omitempty"`
	Secret string `yaml:"secret,omitempty"`
}

type AIModelProps struct {
	Model AIModelPropsModel `yaml:"model,omitempty"`
}

type AIModelPropsModel struct {
	Name    string `yaml:"name,omitempty"`
	Version string `yaml:"version,omitempty"`
}

type CosmosDBProps struct {
	Containers []CosmosDBContainerProps `yaml:"containers,omitempty"`
}

type CosmosDBContainerProps struct {
	Name          string   `yaml:"name,omitempty"`
	PartitionKeys []string `yaml:"partitionKeys,omitempty"`
}

type ServiceBusProps struct {
	Queues []string `yaml:"queues,omitempty"`
	Topics []string `yaml:"topics,omitempty"`
}

type EventHubsProps struct {
	Hubs []string `yaml:"hubs,omitempty"`
}

type StorageProps struct {
	Containers []string `yaml:"containers,omitempty"`
}

type AiServicesModel struct {
	Name    string             `yaml:"name,omitempty"`
	Version string             `yaml:"version,omitempty"`
	Format  string             `yaml:"format,omitempty"`
	Sku     AiServicesModelSku `yaml:"sku,omitempty"`
}

type AiServicesModelSku struct {
	Name      string `yaml:"name,omitempty"`
	UsageName string `yaml:"usageName,omitempty"`
	Capacity  int32  `yaml:"capacity,omitempty"`
}

type AiFoundryModelProps struct {
	Models []AiServicesModel `yaml:"models,omitempty"`
}
