// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"github.com/azure/azure-dev/cli/azd/internal"

	"github.com/braydonk/yaml"
)

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

func (r ResourceType) String() string {
	switch r {
	case ResourceTypeDbRedis:
		return "Redis"
	case ResourceTypeDbPostgres:
		return "PostgreSQL"
	case ResourceTypeDbMySQL:
		return "MySQL"
	case ResourceTypeDbMongo:
		return "MongoDB"
	case ResourceTypeDbCosmos:
		return "CosmosDB"
	case ResourceTypeHostContainerApp:
		return "Container App"
	case ResourceTypeOpenAiModel:
		return "Open AI Model"
	case ResourceTypeMessagingServiceBus:
		return "Service Bus"
	case ResourceTypeMessagingEventHubs:
		return "Event Hubs"
	case ResourceTypeMessagingKafka:
		return "Kafka"
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

	switch raw.Type {
	case ResourceTypeOpenAiModel:
		err := marshalRawProps(raw.Props.(AIModelProps))
		if err != nil {
			return nil, err
		}
	case ResourceTypeHostContainerApp:
		err := marshalRawProps(raw.Props.(ContainerAppProps))
		if err != nil {
			return nil, err
		}
	case ResourceTypeDbMySQL:
		err := marshalRawProps(raw.Props.(MySQLProps))
		if err != nil {
			return nil, err
		}
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
	case ResourceTypeDbMySQL:
		mp := MySQLProps{}
		if err := unmarshalProps(&mp); err != nil {
			return err
		}
		raw.Props = mp
	case ResourceTypeDbPostgres:
		pp := PostgresProps{}
		if err := unmarshalProps(&pp); err != nil {
			return err
		}
		raw.Props = pp
	case ResourceTypeDbMongo:
		mp := MongoDBProps{}
		if err := unmarshalProps(&mp); err != nil {
			return err
		}
		raw.Props = mp
	case ResourceTypeDbCosmos:
		cp := CosmosDBProps{}
		if err := unmarshalProps(&cp); err != nil {
			return err
		}
		raw.Props = cp
	case ResourceTypeMessagingServiceBus:
		sb := ServiceBusProps{}
		if err := unmarshalProps(&sb); err != nil {
			return err
		}
		raw.Props = sb
	case ResourceTypeMessagingEventHubs:
		eh := EventHubsProps{}
		if err := unmarshalProps(&eh); err != nil {
			return err
		}
		raw.Props = eh
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

type MySQLProps struct {
	DatabaseName string            `yaml:"databaseName,omitempty"`
	AuthType     internal.AuthType `yaml:"authType,omitempty"`
}

type PostgresProps struct {
	DatabaseName string            `yaml:"databaseName,omitempty"`
	AuthType     internal.AuthType `yaml:"authType,omitempty"`
}

type MongoDBProps struct {
	DatabaseName string `yaml:"databaseName,omitempty"`
}

type CosmosDBProps struct {
	Containers   []CosmosDBContainerProps `yaml:"containers,omitempty"`
	DatabaseName string                   `yaml:"databaseName,omitempty"`
	AuthType     internal.AuthType        `yaml:"authType,omitempty"`
}

type CosmosDBContainerProps struct {
	ContainerName     string   `yaml:"containerName,omitempty"`
	PartitionKeyPaths []string `yaml:"partitionKeyPaths,omitempty"`
}

type ServiceBusProps struct {
	Queues   []string          `yaml:"queues,omitempty"`
	IsJms    bool              `yaml:"isJms,omitempty"`
	AuthType internal.AuthType `yaml:"authType,omitempty"`
}

type EventHubsProps struct {
	EventHubNames []string          `yaml:"EventHubNames,omitempty"`
	AuthType      internal.AuthType `yaml:"authType,omitempty"`
}

type KafkaProps struct {
	Topics   []string          `yaml:"topics,omitempty"`
	AuthType internal.AuthType `yaml:"authType,omitempty"`
}
