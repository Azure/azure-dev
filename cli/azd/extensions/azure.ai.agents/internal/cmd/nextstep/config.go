// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/pkg/projectconfig"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

type guidanceAgentDefinition struct {
	agent_yaml.AgentDefinition `json:",inline"`
	Protocols                  []agent_yaml.ProtocolVersionRecord `json:"protocols,omitempty"`
	EnvironmentVariables       *[]agent_yaml.EnvironmentVariable  `json:"environmentVariables,omitempty"`
}

type guidanceServiceConfig struct {
	Environment map[string]string    `json:"env,omitempty"`
	Deployments []guidanceDeployment `json:"deployments,omitempty"`
	Toolboxes   []guidanceToolbox    `json:"toolboxes,omitempty"`
	Connections []guidanceConnection `json:"connections,omitempty"`
}

type guidanceDeployment struct {
	Name  string                  `json:"name"`
	Model guidanceDeploymentModel `json:"model"`
}

type guidanceDeploymentModel struct {
	Name string `json:"name"`
}

type guidanceToolbox struct {
	Name string `json:"name"`
}

func (t *guidanceToolbox) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		t.Name = name
		return nil
	}
	type toolbox guidanceToolbox
	var value toolbox
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*t = guidanceToolbox(value)
	return nil
}

type guidanceConnection struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Target   string `json:"target"`
	AuthType string `json:"authType"`
}

func loadGuidanceServiceConfig(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (
	guidanceAgentDefinition,
	guidanceServiceConfig,
	*structpb.Struct,
	error,
) {
	var agentDef guidanceAgentDefinition
	var cfg guidanceServiceConfig
	var fallbackProps *structpb.Struct

	for _, candidate := range []*structpb.Struct{
		svc.GetAdditionalProperties(),
		svc.GetConfig(),
	} {
		if candidate == nil || len(candidate.GetFields()) == 0 {
			continue
		}
		props, err := resolveServiceProps(
			candidate,
			svc.GetName(),
			projectRoot,
		)
		if err != nil {
			return agentDef, cfg, nil, err
		}
		var candidateCfg guidanceServiceConfig
		if err := unmarshalServiceProps(
			props,
			&candidateCfg,
		); err != nil {
			return agentDef, cfg, props, err
		}
		if fallbackProps == nil {
			fallbackProps = props
			cfg = candidateCfg
		}
		if kind := props.GetFields()["kind"]; kind != nil &&
			kind.GetStringValue() != "" {
			if err := unmarshalServiceProps(
				props,
				&agentDef,
			); err != nil {
				return agentDef, candidateCfg, props, err
			}
			if err := mergeServiceEnvironment(
				svc,
				projectRoot,
				&candidateCfg,
			); err != nil {
				return agentDef, candidateCfg, props, err
			}
			return agentDef, candidateCfg, props, nil
		}
	}

	legacy, found, err := loadLegacyAgentDefinition(
		svc,
		projectRoot,
	)
	if err != nil {
		return agentDef, cfg, fallbackProps, err
	}
	if found {
		agentDef = guidanceAgentDefinition{
			AgentDefinition:      legacy.AgentDefinition,
			Protocols:            legacy.Protocols,
			EnvironmentVariables: legacy.EnvironmentVariables,
		}
	}
	if err := mergeServiceEnvironment(
		svc,
		projectRoot,
		&cfg,
	); err != nil {
		return agentDef, cfg, fallbackProps, err
	}
	return agentDef, cfg, fallbackProps, nil
}

func resolveServiceConfigProps(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (*structpb.Struct, error) {
	props := svc.GetAdditionalProperties()
	if props == nil || len(props.GetFields()) == 0 {
		props = svc.GetConfig()
	}
	if props == nil {
		return nil, nil
	}
	return resolveServiceProps(props, svc.GetName(), projectRoot)
}

func resolveServiceProps(
	props *structpb.Struct,
	serviceName string,
	projectRoot string,
) (*structpb.Struct, error) {
	resolved, err := foundry.ResolveFileRefs(
		props.AsMap(),
		projectRoot,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"resolving service %q config: %w",
			serviceName,
			err,
		)
	}
	if err := projectconfig.NormalizeEnvironment(resolved); err != nil {
		return nil, fmt.Errorf(
			"normalizing service %q environment: %w",
			serviceName,
			err,
		)
	}
	out, err := structpb.NewStruct(resolved)
	if err != nil {
		return nil, fmt.Errorf(
			"encoding service %q config: %w",
			serviceName,
			err,
		)
	}
	return out, nil
}

func unmarshalServiceProps(
	props *structpb.Struct,
	out any,
) error {
	data, err := json.Marshal(props.AsMap())
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func mergeServiceEnvironment(
	svc *azdext.ServiceConfig,
	projectRoot string,
	cfg *guidanceServiceConfig,
) error {
	if cfg.Environment == nil {
		cfg.Environment = map[string]string{}
	}
	for key, value := range svc.GetEnvironment() {
		if _, exists := cfg.Environment[key]; !exists {
			cfg.Environment[key] = value
		}
	}

	raw, err := projectconfig.LoadServiceEnvironment(
		projectRoot,
		svc.GetName(),
	)
	if err != nil {
		return err
	}
	maps.Copy(cfg.Environment, raw)
	return nil
}

func loadLegacyAgentDefinition(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (agent_yaml.ContainerAgent, bool, error) {
	if projectRoot == "" {
		return agent_yaml.ContainerAgent{}, false, nil
	}
	for _, name := range []string{"agent.yaml", "agent.yml"} {
		path, err := paths.JoinAllowRoot(
			projectRoot,
			svc.GetRelativePath(),
			name,
		)
		if err != nil {
			return agent_yaml.ContainerAgent{}, false, err
		}
		data, err := os.ReadFile(path) //nolint:gosec
		if err != nil {
			continue
		}
		var agentDef agent_yaml.ContainerAgent
		if err := yaml.Unmarshal(data, &agentDef); err != nil {
			return agent_yaml.ContainerAgent{}, false, err
		}
		return agentDef, true, nil
	}
	return agent_yaml.ContainerAgent{}, false, nil
}
