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
		ResourceTypeDbMongo,
		ResourceTypeDbCosmos,
		ResourceTypeHostContainerApp,
		ResourceTypeOpenAiModel,
		ResourceTypeMessagingEventHubs,
		ResourceTypeMessagingServiceBus,
		ResourceTypeStorage,
	}
}

const (
	ResourceTypeDbRedis             ResourceType = "db.redis"
	ResourceTypeDbPostgres          ResourceType = "db.postgres"
	ResourceTypeDbMongo             ResourceType = "db.mongo"
	ResourceTypeDbCosmos            ResourceType = "db.cosmos"
	ResourceTypeHostContainerApp    ResourceType = "host.containerapp"
	ResourceTypeOpenAiModel         ResourceType = "ai.openai.model"
	ResourceTypeMessagingEventHubs  ResourceType = "messaging.eventhubs"
	ResourceTypeMessagingServiceBus ResourceType = "messaging.servicebus"
	ResourceTypeStorage             ResourceType = "storage"
)

func (r ResourceType) String() string {
	switch r {
	case ResourceTypeDbRedis:
		return "Redis"
	case ResourceTypeDbPostgres:
		return "PostgreSQL"
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
	}

	return ""
}

type ResourceConfig struct {
	// Reference to the parent project configuration
	Project *ProjectConfig `yaml:"-"`
	// Type of resource
	Type ResourceType `yaml:"type"`
	// The name of the resource
	Name string `yaml:"-"`
	// The properties for the resource
	RawProps map[string]yaml.Node `yaml:",inline"`
	Props    interface{}          `yaml:"-"`
	// Relationships to other resources
	Uses []string `yaml:"uses,omitempty"`
}

func (r *ResourceConfig) MarshalYAML() (interface{}, error) {
	type rawResourceConfig ResourceConfig
	raw := rawResourceConfig(*r)

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
	Containers   []CosmosDBContainerProps `yaml:"containers,omitempty"`
	DatabaseName string                   `yaml:"databaseName,omitempty"`
}

type CosmosDBContainerProps struct {
	ContainerName     string   `yaml:"containerName,omitempty"`
	PartitionKeyPaths []string `yaml:"partitionKeyPaths,omitempty"`
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
