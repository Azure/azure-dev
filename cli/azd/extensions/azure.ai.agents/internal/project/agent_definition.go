// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/agentkind"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/pkg/projectconfig"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/braydonk/yaml"
	"google.golang.org/protobuf/types/known/structpb"
)

// AgentDefinitionSource identifies where a loaded agent definition came from.
type AgentDefinitionSource int

const (
	// AgentDefinitionSourceInline means the definition was read from the agent
	// service entry's service-level (inline) properties — the unified shape.
	AgentDefinitionSourceInline AgentDefinitionSource = iota
	// AgentDefinitionSourceLegacyConfig means the definition was read from the
	// deprecated config-nested shape (a populated `config:` on the service).
	AgentDefinitionSourceLegacyConfig
	// AgentDefinitionSourceDisk means the definition was read from a legacy
	// agent.yaml/agent.yml file on disk (the deprecated file-based shape).
	AgentDefinitionSourceDisk
)

// IsLegacy reports whether the source is one of the deprecated shapes (a
// config-nested entry or an on-disk agent.yaml) that callers should warn about.
func (s AgentDefinitionSource) IsLegacy() bool {
	return s == AgentDefinitionSourceLegacyConfig || s == AgentDefinitionSourceDisk
}

// MigrationGuideURL points at guidance for migrating older Foundry agent
// projects onto the unified azure.yaml shape.
const MigrationGuideURL = "https://github.com/Azure/azure-dev/tree/main/cli/azd/extensions/" +
	"azure.ai.agents#migrating-legacy-agent-configuration"

var legacyAgentShapeWarnOnce sync.Once

// WarnLegacyAgentShape prints a one-time deprecation warning when an agent
// definition is read from a deprecated shape — an on-disk agent.yaml or the
// config-nested azure.ai.agent service entry — rather than from the unified
// service-level properties. azd keeps reading the old shape during the
// deprecation window; the warning points the user at the migration guide.
func WarnLegacyAgentShape(source AgentDefinitionSource) {
	if !source.IsLegacy() {
		return
	}
	legacyAgentShapeWarnOnce.Do(func() {
		detail := "the deprecated config-nested azure.ai.agent shape"
		if source == AgentDefinitionSourceDisk {
			detail = "an on-disk agent.yaml/agent.yml"
		}
		fmt.Fprintf(os.Stderr,
			"WARNING: this project uses %s. azd still reads it, but the shape is deprecated; "+
				"re-run `azd ai agent init` to move the agent definition into azure.yaml. See %s\n",
			detail, MigrationGuideURL,
		)
	})
}

// WarnOrphanedConfigEnv warns when a service still declares
// environment variables under the deprecated config-nested env:
// block. azd reads service environment values only from the
// service-level env:, so anything left under config: is ignored by
// both `azd ai agent run` and deploy.
//
// Unlike the deprecated environmentVariables list, nothing migrates
// config.env and no azd command ever wrote it, so without this the
// values would disappear with no other signal.
func WarnOrphanedConfigEnv(svc *azdext.ServiceConfig) {
	names := orphanedConfigEnvNames(svc)
	if len(names) == 0 {
		return
	}
	fmt.Printf("%s\n", output.WithWarningFormat(
		"WARNING: service %q sets %s under the deprecated `config: "+
			"env:` block, which is no longer read and will be "+
			"ignored. Move them to the service-level `env:` so they "+
			"apply to both run and deploy. See %s",
		svc.GetName(), strings.Join(names, ", "), MigrationGuideURL,
	))
}

// orphanedConfigEnvNames returns the variable names a service still
// declares under the deprecated config-nested env: block, sorted.
// It reads svc.Config directly because core binds the service-level
// env: to ServiceConfig.Environment, so an env key can only reach a
// property bag from the nested shape.
func orphanedConfigEnvNames(svc *azdext.ServiceConfig) []string {
	value, found := svc.GetConfig().GetFields()["env"]
	if !found {
		return nil
	}
	fields := value.GetStructValue().GetFields()
	if len(fields) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(fields))
}

// AgentDefinitionInline is the hosted-agent definition (formerly agent.yaml)
// carried as flat service-level properties on the azure.ai.agent service entry.
//
// It mirrors [agent_yaml.ContainerAgent] except for two fields that map onto core
// [azdext.ServiceConfig] fields instead of the inline property bag: the CPU/memory
// Resources (carried in the `container` config to avoid a key/type collision with
// the tool Resources list [ServiceTargetAgentConfig.Resources], also keyed
// `resources`), and Image (carried on the core `image` service field, since
// `image` is a first-class ServiceConfig field that core binds and round-trips).
// The embedded [agent_yaml.AgentDefinition] promotes kind, name, description, and
// the schema fields to the top level.
type AgentDefinitionInline struct {
	agent_yaml.AgentDefinition `json:",inline"`
	Protocols                  []agent_yaml.ProtocolVersionRecord `json:"protocols,omitempty"`
	// EnvironmentVariables reads the deprecated inline shape.
	EnvironmentVariables *[]agent_yaml.EnvironmentVariable `json:"environmentVariables,omitempty"`
	AgentEndpoint        *agent_yaml.AgentEndpoint         `json:"agentEndpoint,omitempty"`
	AgentCard            *agent_yaml.AgentCard             `json:"agentCard,omitempty"`
	CodeConfiguration    *agent_yaml.CodeConfiguration     `json:"codeConfiguration,omitempty"`
	Policies             []agent_yaml.Policy               `json:"policies,omitempty"`
	SessionConfiguration *agent_yaml.SessionConfiguration  `json:"sessionConfiguration,omitempty"`

	// Voice-agent fields (kind: prompt-voice). All omitempty so container/
	// workflow entries are byte-for-byte unchanged.
	ModelType    agent_yaml.VoiceModelType `json:"modelType,omitempty"`
	Model        *agent_yaml.Model         `json:"model,omitempty"`
	Instructions *string                   `json:"instructions,omitempty"`
	Voice        *string                   `json:"voice,omitempty"`
	Store        *bool                     `json:"store,omitempty"`
}

// voiceAgentDefinitionToInline projects a VoiceAgent into the inline definition
// written to azure.yaml. Voice agents carry no container/image/code config.
func voiceAgentDefinitionToInline(va agent_yaml.VoiceAgent) AgentDefinitionInline {
	return AgentDefinitionInline{
		AgentDefinition: va.AgentDefinition,
		ModelType:       va.ModelType,
		Model:           va.Model,
		Instructions:    va.Instructions,
		Voice:           va.Voice,
		Store:           va.Store,
	}
}

// toVoiceAgent rebuilds an agent_yaml.VoiceAgent from the inline definition.
func (d AgentDefinitionInline) toVoiceAgent() agent_yaml.VoiceAgent {
	return agent_yaml.VoiceAgent{
		AgentDefinition: d.AgentDefinition,
		ModelType:       d.ModelType,
		Model:           d.Model,
		Instructions:    d.Instructions,
		Voice:           d.Voice,
		Store:           d.Store,
	}
}

// agentDefinitionToInline splits a ContainerAgent into the inline definition,
// the CPU/memory ContainerSettings (carried in the `container` config), and the
// prebuilt image (carried on the core `image` service field). The latter two are
// returned separately so the caller can place them on their respective homes.
func agentDefinitionToInline(ca agent_yaml.ContainerAgent) (AgentDefinitionInline, *ContainerSettings, string) {
	inline := AgentDefinitionInline{
		AgentDefinition:      ca.AgentDefinition,
		Protocols:            ca.Protocols,
		AgentEndpoint:        ca.AgentEndpoint,
		AgentCard:            ca.AgentCard,
		CodeConfiguration:    ca.CodeConfiguration,
		Policies:             ca.Policies,
		SessionConfiguration: ca.SessionConfiguration,
	}

	var container *ContainerSettings
	if ca.Resources != nil {
		container = &ContainerSettings{
			Resources: &ResourceSettings{Cpu: ca.Resources.Cpu, Memory: ca.Resources.Memory},
		}
	}

	return inline, container, ca.Image
}

// toContainerAgent rebuilds the agent_yaml.ContainerAgent from the inline
// definition, the CPU/memory carried in the `container` config, and the image
// carried on the core service field.
func (d AgentDefinitionInline) toContainerAgent(
	container *ContainerSettings,
	image string,
	environment map[string]string,
) agent_yaml.ContainerAgent {
	environmentVariables := d.EnvironmentVariables
	if len(environment) > 0 {
		legacyEnvironment := AgentEnvironment(agent_yaml.ContainerAgent{
			EnvironmentVariables: d.EnvironmentVariables,
		})
		if legacyEnvironment == nil {
			legacyEnvironment = map[string]string{}
		}
		maps.Copy(legacyEnvironment, environment)
		environmentVariables = environmentVariablesFromMap(legacyEnvironment)
	}

	ca := agent_yaml.ContainerAgent{
		AgentDefinition:      d.AgentDefinition,
		Image:                image,
		Protocols:            d.Protocols,
		EnvironmentVariables: environmentVariables,
		AgentEndpoint:        d.AgentEndpoint,
		AgentCard:            d.AgentCard,
		CodeConfiguration:    d.CodeConfiguration,
		Policies:             d.Policies,
		SessionConfiguration: d.SessionConfiguration,
	}

	if container != nil && container.Resources != nil {
		ca.Resources = &agent_yaml.ContainerResources{
			Cpu:    container.Resources.Cpu,
			Memory: container.Resources.Memory,
		}
	}

	return ca
}

// AgentEnvironment converts an agent environment list to a map.
func AgentEnvironment(ca agent_yaml.ContainerAgent) map[string]string {
	if ca.EnvironmentVariables == nil || len(*ca.EnvironmentVariables) == 0 {
		return nil
	}

	environment := make(map[string]string, len(*ca.EnvironmentVariables))
	for _, variable := range *ca.EnvironmentVariables {
		environment[variable.Name] = variable.Value
	}
	return environment
}

// ResolveAgentEnvironmentVariable preserves values forwarded by core.
// A name the service declares in env: wins outright. Every other
// name expands through mapping, which both callers back with the
// full azd environment even when the service declares env:.
//
// That project fallback is deliberate. environment_variables is
// deprecated but additive: mergeAgentRunEnvironment lets env:
// override a same-named entry and keeps the rest, so a ${FOO} an
// author wrote there stays resolvable mid-migration. Dropping the
// fallback would turn it into an empty string with no error.
//
// The connections extension does drop it once env: is declared
// (connectionEnvironmentMapping in azure.ai.projects), because
// those values become ARM parameters at provision time rather
// than runtime values for a container the author owns.
func ResolveAgentEnvironmentVariable(
	name string,
	value string,
	serviceEnvironment map[string]string,
	mapping func(string) string,
) (string, error) {
	if environmentValue, found := serviceEnvironment[name]; found {
		return environmentValue, nil
	}
	return ExpandEnv(value, func(variableName string) string {
		if environmentValue, found := serviceEnvironment[variableName]; found {
			return environmentValue
		}
		if mapping == nil {
			return ""
		}
		return mapping(variableName)
	})
}

func environmentVariablesFromMap(
	environment map[string]string,
) *[]agent_yaml.EnvironmentVariable {
	if len(environment) == 0 {
		return nil
	}

	variables := make(
		[]agent_yaml.EnvironmentVariable,
		0,
		len(environment),
	)
	for _, name := range slices.Sorted(maps.Keys(environment)) {
		variables = append(variables, agent_yaml.EnvironmentVariable{
			Name:  name,
			Value: environment[name],
		})
	}
	return &variables
}

// structHasKind reports whether the struct carries a non-empty string `kind`,
// the marker that an agent definition is present in a service entry's inline or
// config properties.
func structHasKind(s *structpb.Struct) bool {
	if s == nil {
		return false
	}
	v, ok := s.Fields["kind"]
	if !ok {
		return false
	}
	return v.GetStringValue() != ""
}

// LoadAgentDefinition resolves the hosted-agent definition for an azure.ai.agent
// service. It prefers the unified inline shape (service-level properties), falls
// back to the deprecated config-nested shape, and finally to a legacy
// agent.yaml/agent.yml file on disk so older projects keep building and
// deploying during the deprecation window.
//
// It returns the parsed ContainerAgent, whether it is a hosted agent (false for
// other kinds), and the source the definition came from (see
// [AgentDefinitionSource.IsLegacy]).
func LoadAgentDefinition(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (agent_yaml.ContainerAgent, bool, AgentDefinitionSource, error) {
	ca, isHosted, found, source, err :=
		AgentDefinitionFromResolvedService(svc, projectRoot)
	if err != nil {
		return agent_yaml.ContainerAgent{}, false, source, err
	}
	if found {
		return ca, isHosted, source, nil
	}

	return agentDefinitionFromDisk(svc, projectRoot)
}

// AgentDefinitionFromResolvedService expands local file includes.
func AgentDefinitionFromResolvedService(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (
	agent_yaml.ContainerAgent,
	bool,
	bool,
	AgentDefinitionSource,
	error,
) {
	candidates := []struct {
		props  *structpb.Struct
		source AgentDefinitionSource
	}{
		{svc.GetAdditionalProperties(), AgentDefinitionSourceInline},
		{svc.GetConfig(), AgentDefinitionSourceLegacyConfig},
	}
	for _, candidate := range candidates {
		if candidate.props == nil ||
			len(candidate.props.GetFields()) == 0 {
			continue
		}
		resolved, err := resolveServiceProps(
			candidate.props,
			svc.GetName(),
			projectRoot,
		)
		if err != nil {
			return agent_yaml.ContainerAgent{},
				false,
				false,
				candidate.source,
				err
		}
		if !structHasKind(resolved) {
			continue
		}
		image := svc.GetImage()
		if image == "" {
			if value := resolved.GetFields()["image"]; value != nil {
				image = value.GetStringValue()
			}
		}
		ca, isHosted, err := agentDefinitionFromStruct(
			resolved,
			image,
			svc.GetEnvironment(),
		)
		return ca, isHosted, true, candidate.source, err
	}

	return agent_yaml.ContainerAgent{},
		false,
		false,
		AgentDefinitionSourceInline,
		nil
}

// AgentDefinitionUsesFileRef reports whether a root $ref supplies the
// agent definition.
func AgentDefinitionUsesFileRef(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (bool, error) {
	for _, props := range []*structpb.Struct{
		svc.GetAdditionalProperties(),
		svc.GetConfig(),
	} {
		if props == nil || props.GetFields()["$ref"] == nil {
			continue
		}
		refOnly := &structpb.Struct{Fields: map[string]*structpb.Value{
			"$ref": props.GetFields()["$ref"],
		}}
		resolved, err := resolveServiceProps(
			refOnly,
			svc.GetName(),
			projectRoot,
		)
		if err != nil {
			return false, err
		}
		if structHasKind(resolved) {
			return true, nil
		}
	}
	return false, nil
}

// AgentDefinitionFromService returns the agent definition carried inline on the
// service entry — the unified service-level shape, or the deprecated
// config-nested shape. found is false when the entry carries no inline
// definition, in which case callers fall back to a legacy agent.yaml on disk.
func AgentDefinitionFromService(
	svc *azdext.ServiceConfig,
) (agent_yaml.ContainerAgent, bool, bool, AgentDefinitionSource, error) {
	inlineStruct := svc.GetAdditionalProperties()
	source := AgentDefinitionSourceInline
	if !structHasKind(inlineStruct) {
		if cfg := svc.GetConfig(); structHasKind(cfg) {
			inlineStruct = cfg
			source = AgentDefinitionSourceLegacyConfig
		} else {
			return agent_yaml.ContainerAgent{}, false, false, source, nil
		}
	}

	ca, isHosted, err := agentDefinitionFromStruct(
		inlineStruct,
		svc.GetImage(),
		svc.GetEnvironment(),
	)
	return ca, isHosted, true, source, err
}

// LoadServiceTargetAgentConfig reads the agent service's deploy/provision config
// (container settings, tool resources, tool connections, startup command, and —
// for pre-split projects — bundled deployments/connections/toolboxes) from the
// service-level properties, falling back to the deprecated config-nested shape.
func LoadServiceTargetAgentConfig(svc *azdext.ServiceConfig) (*ServiceTargetAgentConfig, error) {
	s := ServiceConfigProps(svc)
	cfg := &ServiceTargetAgentConfig{}
	if s == nil {
		return cfg, nil
	}
	if err := UnmarshalStruct(s, &cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ServiceConfigProps returns the agent service's service-level (inline)
// properties when present, otherwise the deprecated config-nested struct. It is
// the single accessor for code that needs the raw property struct regardless of
// which shape a project uses.
func ServiceConfigProps(svc *azdext.ServiceConfig) *structpb.Struct {
	if s := svc.GetAdditionalProperties(); s != nil && len(s.GetFields()) > 0 {
		if svc.GetHost() == "azure.ai.agent" &&
			!structHasKind(s) &&
			structHasKind(svc.GetConfig()) {
			return svc.GetConfig()
		}
		return s
	}
	return svc.GetConfig()
}

// ResolveServiceConfigProps expands local $ref file includes.
func ResolveServiceConfigProps(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (*structpb.Struct, error) {
	props := ServiceConfigProps(svc)
	if props == nil {
		return nil, nil
	}
	return resolveServiceProps(props, svc.GetName(), projectRoot)
}

// ResolveServiceConfigInPlace expands local file references in both
// service-level properties and legacy config. It also normalizes
// environment scalars so consumers receive an effective config.
func ResolveServiceConfigInPlace(
	svc *azdext.ServiceConfig,
	projectRoot string,
) error {
	if props := svc.GetAdditionalProperties(); props != nil &&
		len(props.GetFields()) > 0 {
		resolved, err := resolveServiceProps(
			props,
			svc.GetName(),
			projectRoot,
		)
		if err != nil {
			return err
		}
		svc.AdditionalProperties = resolved
	}
	if config := svc.GetConfig(); config != nil &&
		len(config.GetFields()) > 0 {
		resolved, err := resolveServiceProps(
			config,
			svc.GetName(),
			projectRoot,
		)
		if err != nil {
			return err
		}
		svc.Config = resolved
	}
	return nil
}

// NormalizeServiceConfigInPlace converts environment scalars in both
// service-level properties and legacy config.
// File references remain intact for persistence.
func NormalizeServiceConfigInPlace(svc *azdext.ServiceConfig) error {
	if props := svc.GetAdditionalProperties(); props != nil &&
		len(props.GetFields()) > 0 {
		normalized, err := normalizeServiceProps(props, svc.GetName())
		if err != nil {
			return err
		}
		svc.AdditionalProperties = normalized
	}
	if config := svc.GetConfig(); config != nil &&
		len(config.GetFields()) > 0 {
		normalized, err := normalizeServiceProps(config, svc.GetName())
		if err != nil {
			return err
		}
		svc.Config = normalized
	}
	return nil
}

func resolveServiceProps(
	props *structpb.Struct,
	serviceName string,
	projectRoot string,
) (*structpb.Struct, error) {
	if err := validateRootRefCoreFields(props, projectRoot); err != nil {
		return nil, fmt.Errorf(
			"validating service %q config: %w",
			serviceName,
			err,
		)
	}
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
	return normalizedServiceProps(resolved, serviceName)
}

func normalizeServiceProps(
	props *structpb.Struct,
	serviceName string,
) (*structpb.Struct, error) {
	return normalizedServiceProps(props.AsMap(), serviceName)
}

func normalizedServiceProps(
	values map[string]any,
	serviceName string,
) (*structpb.Struct, error) {
	if err := projectconfig.NormalizeEnvironment(values); err != nil {
		return nil, fmt.Errorf(
			"normalizing service %q environment: %w",
			serviceName,
			err,
		)
	}

	out, err := structpb.NewStruct(values)
	if err != nil {
		return nil, fmt.Errorf(
			"encoding normalized service %q config: %w",
			serviceName,
			err,
		)
	}
	return out, nil
}

func validateRootRefCoreFields(
	props *structpb.Struct,
	projectRoot string,
) error {
	ref := props.GetFields()["$ref"]
	if ref == nil || ref.GetStringValue() == "" {
		return nil
	}
	referenced, err := foundry.ResolveFileRefs(
		map[string]any{"$ref": ref.GetStringValue()},
		projectRoot,
	)
	if err != nil {
		return err
	}
	for _, field := range []string{
		"env",
		"project",
		"language",
		"image",
		"docker",
	} {
		if _, found := referenced[field]; found {
			return fmt.Errorf(
				"root $ref must not provide core field %q; declare it in azure.yaml",
				field,
			)
		}
	}
	return nil
}

// UpsertAgentEnvVars updates the service-level environment map.
func UpsertAgentEnvVars(svc *azdext.ServiceConfig, kv map[string]string) error {
	ca, _, found, source, err := AgentDefinitionFromService(svc)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("service %q does not carry an inline agent definition", svc.GetName())
	}

	environment := AgentEnvironment(ca)
	if environment == nil {
		environment = map[string]string{}
	}
	maps.Copy(environment, kv)
	svc.Environment = environment

	props := svc.GetAdditionalProperties()
	if source == AgentDefinitionSourceLegacyConfig {
		props = svc.GetConfig()
	}
	if props != nil {
		delete(props.Fields, "environmentVariables")
	}
	return nil
}

// InlineAgentEnvironmentVariables returns the deprecated inline
// environmentVariables carried on the agent definition as a raw
// template map, without merging the core-forwarded (already expanded)
// service environment. Values are the templates as authored, suitable
// for migrating into the env section without losing them.
func InlineAgentEnvironmentVariables(
	svc *azdext.ServiceConfig,
) (map[string]string, error) {
	props := ServiceConfigProps(svc)
	if props == nil || len(props.GetFields()) == 0 {
		return nil, nil
	}
	var inline AgentDefinitionInline
	if err := UnmarshalStruct(props, &inline); err != nil {
		return nil, err
	}
	return AgentEnvironment(agent_yaml.ContainerAgent{
		EnvironmentVariables: inline.EnvironmentVariables,
	}), nil
}

// SetAgentContainerSettings writes the resolved container settings onto the
// agent service's inline properties, preserving every other key (the agent
// definition and the rest of the deploy/provision config). It mutates whichever
// shape the service uses (the unified AdditionalProperties, or — for older
// projects — the config-nested struct).
func SetAgentContainerSettings(
	svc *azdext.ServiceConfig,
	container *ContainerSettings,
) error {
	props := ServiceConfigProps(svc)
	legacy := props != nil && props == svc.GetConfig()
	if props == nil {
		props = &structpb.Struct{}
	}
	if props.Fields == nil {
		props.Fields = map[string]*structpb.Value{}
	}

	containerStruct, err := MarshalStruct(container)
	if err != nil {
		return fmt.Errorf("marshaling container settings: %w", err)
	}
	props.Fields["container"] = structpb.NewStructValue(containerStruct)

	if legacy {
		svc.Config = props
	} else {
		svc.AdditionalProperties = props
	}
	return nil
}

// agentDefinitionFromStruct builds the ContainerAgent from an inline/config
// struct that carries the agent definition as service-level properties. coreImage
// is the value of the service's `image` field, which is carried on the core
// [azdext.ServiceConfig] rather than in the inline property bag.
func agentDefinitionFromStruct(
	s *structpb.Struct,
	coreImage string,
	environment map[string]string,
) (agent_yaml.ContainerAgent, bool, error) {
	var inline AgentDefinitionInline
	if err := UnmarshalStruct(s, &inline); err != nil {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent service config is not valid: %s", err),
			"re-run `azd ai agent init` to regenerate the agent service entry",
		)
	}

	if inline.Kind != agent_yaml.AgentKindHosted {
		if err := validateAgentServiceDefinition(s.AsMap()); err != nil {
			return agent_yaml.ContainerAgent{}, false, err
		}
		return agent_yaml.ContainerAgent{}, false, nil
	}

	var cfg ServiceTargetAgentConfig
	if err := UnmarshalStruct(s, &cfg); err != nil {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("agent service config is not valid: %s", err),
			"re-run `azd ai agent init` to regenerate the agent service entry",
		)
	}

	ca := inline.toContainerAgent(cfg.Container, coreImage, environment)

	if err := validateAgentServiceDefinition(ca); err != nil {
		return agent_yaml.ContainerAgent{}, false, err
	}

	if ca.Image != "" && !containerImageRefRe.MatchString(ca.Image) {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("invalid container image reference in agent service config: %q", ca.Image),
			"use a valid image reference, e.g. 'myregistry.azurecr.io/image:v1'",
		)
	}

	return ca, true, nil
}

func validateAgentServiceDefinition(definition any) error {
	defBytes, err := yaml.Marshal(definition)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf(
				"agent service definition is not valid: failed to marshal: %s",
				err,
			),
			"fix the agent service entry in azure.yaml or re-run `azd ai agent init`",
		)
	}
	if err := agent_yaml.ValidateAgentDefinition(defBytes); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent service definition is not valid: %s", err),
			"fix the agent service entry in azure.yaml or re-run `azd ai agent init`",
		)
	}
	return nil
}

// agentDefinitionFromDisk reads a legacy agent.yaml/agent.yml from the service
// directory. This is the deprecation fallback for projects written before the
// definition moved into azure.yaml.
func agentDefinitionFromDisk(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (agent_yaml.ContainerAgent, bool, AgentDefinitionSource, error) {
	for _, name := range []string{"agent.yaml", "agent.yml"} {
		defPath, err := paths.JoinAllowRoot(projectRoot, svc.GetRelativePath(), name)
		if err != nil {
			return agent_yaml.ContainerAgent{}, false, AgentDefinitionSourceDisk, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("invalid service path for %s: %s", svc.GetName(), err),
				"update azure.yaml so the agent service path stays within the project directory",
			)
		}
		data, err := os.ReadFile(defPath) //nolint:gosec // path derived from azd project config
		if err != nil {
			continue
		}
		ca, isHosted, err := parseContainerAgentYAML(data)
		return ca, isHosted, AgentDefinitionSourceDisk, err
	}

	return agent_yaml.ContainerAgent{}, false, AgentDefinitionSourceDisk, exterrors.Dependency(
		exterrors.CodeAgentDefinitionNotFound,
		fmt.Sprintf("agent definition not found for service %q", svc.GetName()),
		"re-run `azd ai agent init` to write the agent definition into azure.yaml",
	)
}

// parseContainerAgentYAML validates and parses agent.yaml bytes into a
// ContainerAgent, mirroring the on-disk loader used before the unified shape.
func parseContainerAgentYAML(data []byte) (agent_yaml.ContainerAgent, bool, error) {
	if err := agent_yaml.ValidateAgentDefinition(data); err != nil {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent.yaml is not valid: %s", err),
			"fix the agent.yaml file according to the schema",
		)
	}

	var genericTemplate map[string]any
	if err := yaml.Unmarshal(data, &genericTemplate); err != nil {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("YAML content is not valid: %s", err),
			"verify the agent.yaml has valid YAML syntax",
		)
	}

	kind, ok := genericTemplate["kind"].(string)
	if !ok {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeMissingAgentKind,
			"kind field is missing or not a valid string in agent.yaml",
			"add a valid 'kind' field (e.g., 'hosted') to agent.yaml",
		)
	}

	if kind != string(agent_yaml.AgentKindHosted) {
		return agent_yaml.ContainerAgent{}, false, nil
	}

	var agentDef agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &agentDef); err != nil {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("YAML content is not valid for hosted agent: %s", err),
			"fix the agent.yaml to match the hosted agent schema",
		)
	}

	if agentDef.Image != "" && !containerImageRefRe.MatchString(agentDef.Image) {
		return agent_yaml.ContainerAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("invalid container image reference in agent.yaml: %q", agentDef.Image),
			"use a valid image reference, e.g. 'myregistry.azurecr.io/image:v1'",
		)
	}

	return agentDef, true, nil
}

// voiceAgentFromDefinitionFile reads an agent definition file (an
// AGENT_DEFINITION_PATH override) and reports whether it declares a prompt-voice
// agent, returning the parsed VoiceAgent when it does. A non-voice (e.g. hosted)
// definition returns found=false with no error so the caller can fall through to
// the container path, mirroring VoiceAgentFromResolvedService's contract. This
// lets an explicit override drive the voice/container dispatch with the same
// precedence loadContainerAgentDefinition documents.
func voiceAgentFromDefinitionFile(path string) (agent_yaml.VoiceAgent, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return agent_yaml.VoiceAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to read agent manifest file: %s", err),
			"verify the agent.yaml file exists and is readable",
		)
	}

	var genericTemplate map[string]any
	if err := yaml.Unmarshal(data, &genericTemplate); err != nil {
		return agent_yaml.VoiceAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("YAML content is not valid: %s", err),
			"verify the agent.yaml has valid YAML syntax",
		)
	}

	if kind, _ := genericTemplate["kind"].(string); kind != string(agent_yaml.AgentKindPromptVoice) {
		// Not a voice definition; let the container path handle the override.
		return agent_yaml.VoiceAgent{}, false, nil
	}

	if err := agent_yaml.ValidateAgentDefinition(data); err != nil {
		return agent_yaml.VoiceAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent.yaml is not valid: %s", err),
			"fix the agent.yaml file according to the schema",
		)
	}

	var va agent_yaml.VoiceAgent
	if err := yaml.Unmarshal(data, &va); err != nil {
		return agent_yaml.VoiceAgent{}, false, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("YAML content is not valid for a voice agent: %s", err),
			"fix the agent.yaml to match the prompt-voice schema",
		)
	}

	return va, true, nil
}

// resolveVoiceAgentForDeploy determines whether the service should deploy a
// prompt-voice agent, honoring the AGENT_DEFINITION_PATH override precedence: an
// explicit override file wins over the service entry (matching
// loadContainerAgentDefinition). When agentDefinitionPath is empty the resolved
// service entry is inspected instead. A non-voice result returns found=false so
// the caller falls through to the container deploy path unchanged.
//
// The voice/non-voice decision is delegated to the shared agentkind lookup so
// deploy, Endpoints, and next-step all classify a service identically; this
// function then parses the definition from whichever source agentkind matched.
func resolveVoiceAgentForDeploy(
	agentDefinitionPath string,
	svc *azdext.ServiceConfig,
	projectRoot string,
) (agent_yaml.VoiceAgent, bool, error) {
	isVoice, err := agentkind.IsPromptVoice(svc, projectRoot, agentDefinitionPath)
	if err != nil {
		return agent_yaml.VoiceAgent{}, false, err
	}
	if !isVoice {
		return agent_yaml.VoiceAgent{}, false, nil
	}
	if agentDefinitionPath != "" {
		return voiceAgentFromDefinitionFile(agentDefinitionPath)
	}
	return VoiceAgentFromResolvedService(svc, projectRoot)
}

// service-level properties (and the `container` CPU/memory config) used by the
// unified azure.ai.agent service entry. The returned struct is merged into the
// service entry's AdditionalProperties at init time.
//
// Note: the agent's prebuilt `image` is NOT included here — it maps onto the core
// [azdext.ServiceConfig.Image] field, which the caller must set from ca.Image so
// it round-trips through azure.yaml.
func AgentDefinitionToServiceProperties(
	ca agent_yaml.ContainerAgent,
	extra *ServiceTargetAgentConfig,
) (*structpb.Struct, error) {
	inline, container, _ := agentDefinitionToInline(ca)

	defStruct, err := MarshalStruct(&inline)
	if err != nil {
		return nil, fmt.Errorf("marshaling agent definition: %w", err)
	}

	cfg := ServiceTargetAgentConfig{}
	if extra != nil {
		cfg = *extra
	}
	if container != nil {
		cfg.Container = container
	}

	cfgStruct, err := MarshalStruct(&cfg)
	if err != nil {
		return nil, fmt.Errorf("marshaling agent service config: %w", err)
	}

	// Merge the deploy/provision config keys onto the definition keys. The two
	// sets are disjoint except `container`, which only the config carries.
	maps.Copy(defStruct.Fields, cfgStruct.GetFields())

	return defStruct, nil
}

// VoiceAgentDefinitionToServiceProperties marshals a VoiceAgent (kind:
// prompt-voice) into the inline service-level properties written to azure.yaml.
// Voice agents carry no container/image/code config, so — unlike the container
// writer — there is no `container` block to merge. The optional extra config is
// still merged so provision-time settings (env, etc.) round-trip.
func VoiceAgentDefinitionToServiceProperties(
	va agent_yaml.VoiceAgent,
	extra *ServiceTargetAgentConfig,
) (*structpb.Struct, error) {
	inline := voiceAgentDefinitionToInline(va)

	defStruct, err := MarshalStruct(&inline)
	if err != nil {
		return nil, fmt.Errorf("marshaling voice agent definition: %w", err)
	}

	if extra != nil {
		cfgStruct, err := MarshalStruct(extra)
		if err != nil {
			return nil, fmt.Errorf("marshaling voice agent service config: %w", err)
		}
		maps.Copy(defStruct.Fields, cfgStruct.GetFields())
	}

	return defStruct, nil
}

// VoiceAgentFromResolvedService resolves a prompt-voice agent definition from a
// service entry's inline (preferred) or legacy config properties. It returns the
// parsed VoiceAgent and whether a prompt-voice definition was found. Non-voice
// (or absent) definitions return found=false with no error so callers can fall
// through to the container path unchanged.
func VoiceAgentFromResolvedService(
	svc *azdext.ServiceConfig,
	projectRoot string,
) (agent_yaml.VoiceAgent, bool, error) {
	candidates := []*structpb.Struct{
		svc.GetAdditionalProperties(),
		svc.GetConfig(),
	}
	for _, props := range candidates {
		if props == nil || len(props.GetFields()) == 0 {
			continue
		}
		resolved, err := resolveServiceProps(props, svc.GetName(), projectRoot)
		if err != nil {
			return agent_yaml.VoiceAgent{}, false, err
		}
		if !structHasKind(resolved) {
			continue
		}
		if resolved.GetFields()["kind"].GetStringValue() !=
			string(agent_yaml.AgentKindPromptVoice) {
			// A definition is present but it is not a voice agent.
			return agent_yaml.VoiceAgent{}, false, nil
		}

		var inline AgentDefinitionInline
		if err := UnmarshalStruct(resolved, &inline); err != nil {
			return agent_yaml.VoiceAgent{}, false, exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("voice agent service config is not valid: %s", err),
				"re-run `azd ai agent init` to regenerate the agent service entry",
			)
		}
		return inline.toVoiceAgent(), true, nil
	}

	return agent_yaml.VoiceAgent{}, false, nil
}
