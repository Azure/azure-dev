// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"

	"github.com/braydonk/yaml"
)

type ResourceType string

const (
	ResourceTypeDbRedis          ResourceType = "db.redis"
	ResourceTypeDbPostgres       ResourceType = "db.postgres"
	ResourceTypeDbMySQL          ResourceType = "db.mysql"
	ResourceTypeDbMongo          ResourceType = "db.mongo"
	ResourceTypeHostContainerApp ResourceType = "host.containerapp"
	ResourceTypeOpenAiModel      ResourceType = "ai.openai.model"
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
	DatabaseName string `yaml:"databaseName,omitempty"`
	AuthType     string `yaml:"authType,omitempty"`
}
