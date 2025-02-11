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
	}
}

const (
	ResourceTypeDbRedis          ResourceType = "db.redis"
	ResourceTypeDbPostgres       ResourceType = "db.postgres"
	ResourceTypeDbMongo          ResourceType = "db.mongo"
	ResourceTypeDbCosmos         ResourceType = "db.cosmos"
	ResourceTypeHostContainerApp ResourceType = "host.containerapp"
	ResourceTypeOpenAiModel      ResourceType = "ai.openai.model"
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
