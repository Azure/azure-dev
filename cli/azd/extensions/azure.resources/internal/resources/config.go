// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package resources owns resource definitions and Bicep synthesis for the extension.
package resources

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"

	"azure.resources/internal/resources/scaffold"
)

const (
	ProviderName = "resources"
	Redis        = "db.redis"
	Mongo        = "db.mongo"
	Cosmos       = "db.cosmos"
	ContainerApp = "host.containerapp"
	AppService   = "host.appservice"
	OpenAIModel  = "ai.openai.model"
	EventHubs    = "messaging.eventhubs"
	ServiceBus   = "messaging.servicebus"
	Storage      = "storage"
	AIProject    = "ai.project"
	AISearch     = "ai.search"
	KeyVault     = "keyvault"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
var validEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateEntryName checks a name before embedding it in generated Bicep.
func ValidateEntryName(name string) error {
	if name == "" {
		return nil
	}
	if !validName.MatchString(name) {
		return fmt.Errorf("infrastructure name %q must start with a lowercase letter "+
			"and contain up to 40 lowercase letters, digits or hyphens",
			name)
	}
	return nil
}

// Definition describes a resource using the same inline JSON property shape as azure.yaml.
type Definition struct {
	Type              string                   `json:"type"`
	Uses              []string                 `json:"uses,omitempty"`
	Existing          bool                     `json:"existing,omitempty"`
	Port              int                      `json:"port,omitempty"`
	Env               []ServiceEnvVar          `json:"env,omitempty"`
	Runtime           AppServiceRuntime        `json:"runtime"`
	StartupCommand    string                   `json:"startupCommand,omitempty"`
	Model             AIModelPropsModel        `json:"model"`
	Queues            []string                 `json:"queues,omitempty"`
	Topics            []string                 `json:"topics,omitempty"`
	Hubs              []string                 `json:"hubs,omitempty"`
	Models            []AiServicesModel        `json:"models,omitempty"`
	Containers        []CosmosDBContainerProps `json:"-"`
	StorageContainers []string                 `json:"-"`
}

type ServiceEnvVar struct {
	Name   string `json:"name,omitempty"`
	Value  string `json:"value,omitempty"`
	Secret string `json:"secret,omitempty"`
}

type AppServiceRuntime struct {
	Stack   string `json:"stack,omitempty"`
	Version string `json:"version,omitempty"`
}

type AIModelPropsModel struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type CosmosDBContainerProps struct {
	Name          string   `json:"name,omitempty"`
	PartitionKeys []string `json:"partitionKeys,omitempty"`
}

type AiServicesModel struct {
	Name    string             `json:"name,omitempty"`
	Version string             `json:"version,omitempty"`
	Format  string             `json:"format,omitempty"`
	Sku     AiServicesModelSku `json:"sku"`
}

type AiServicesModelSku struct {
	Name      string `json:"name,omitempty"`
	UsageName string `json:"usageName,omitempty"`
	Capacity  int32  `json:"capacity,omitempty"`
}

type definitionJSON struct {
	Type           string             `json:"type"`
	Uses           []string           `json:"uses,omitempty"`
	Existing       bool               `json:"existing,omitempty"`
	Port           int                `json:"port,omitempty"`
	Env            []ServiceEnvVar    `json:"env,omitempty"`
	Runtime        *AppServiceRuntime `json:"runtime,omitempty"`
	StartupCommand string             `json:"startupCommand,omitempty"`
	Model          *AIModelPropsModel `json:"model,omitempty"`
	Queues         []string           `json:"queues,omitempty"`
	Topics         []string           `json:"topics,omitempty"`
	Hubs           []string           `json:"hubs,omitempty"`
	Models         []AiServicesModel  `json:"models,omitempty"`
	Containers     json.RawMessage    `json:"containers,omitempty"`
}

// UnmarshalJSON decodes the type-specific shape of the shared containers property.
func (d *Definition) UnmarshalJSON(data []byte) error {
	var raw definitionJSON
	if err := decodeStrictJSON(data, &raw); err != nil {
		return err
	}
	*d = Definition{
		Type: raw.Type, Uses: raw.Uses, Existing: raw.Existing, Port: raw.Port, Env: raw.Env,
		StartupCommand: raw.StartupCommand, Queues: raw.Queues, Topics: raw.Topics, Hubs: raw.Hubs,
		Models: raw.Models,
	}
	if raw.Runtime != nil {
		d.Runtime = *raw.Runtime
	}
	if raw.Model != nil {
		d.Model = *raw.Model
	}
	if len(raw.Containers) == 0 {
		return nil
	}
	if d.Type == Storage {
		return decodeStrictJSON(raw.Containers, &d.StorageContainers)
	}
	if d.Type == "" && len(raw.Containers) > 0 && raw.Containers[0] == '[' {
		var values []json.RawMessage
		if err := json.Unmarshal(raw.Containers, &values); err != nil {
			return err
		}
		if len(values) > 0 && len(values[0]) > 0 && values[0][0] == '"' {
			return decodeStrictJSON(raw.Containers, &d.StorageContainers)
		}
	}
	return decodeStrictJSON(raw.Containers, &d.Containers)
}

// MarshalJSON encodes the type-specific shape of the shared containers property.
func (d Definition) MarshalJSON() ([]byte, error) {
	raw := definitionJSON{
		Type: d.Type, Uses: d.Uses, Existing: d.Existing, Port: d.Port, Env: d.Env,
		StartupCommand: d.StartupCommand, Queues: d.Queues, Topics: d.Topics, Hubs: d.Hubs, Models: d.Models,
	}
	if d.Runtime != (AppServiceRuntime{}) {
		raw.Runtime = &d.Runtime
	}
	if d.Model != (AIModelPropsModel{}) {
		raw.Model = &d.Model
	}
	var err error
	if d.Type == Storage {
		if len(d.StorageContainers) > 0 {
			raw.Containers, err = json.Marshal(d.StorageContainers)
		}
	} else if len(d.Containers) > 0 {
		raw.Containers, err = json.Marshal(d.Containers)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(raw)
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

// Config contains entry-local resource definitions.
type Config map[string]Definition

// Decode validates the provider's opaque configuration without depending on core's project model.
func Decode(raw map[string]any) (Config, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encoding resource config: %w", err)
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decoding resource config: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decoding resource config: multiple JSON values")
		}
		return nil, fmt.Errorf("decoding resource config: %w", err)
	}
	return config, config.Validate()
}

// Validate checks resource types, properties, relationships, and generator limits.
func (c Config) Validate() error {
	if len(c) == 0 {
		return fmt.Errorf("resource config must contain at least one resource")
	}

	for _, name := range slices.Sorted(maps.Keys(c)) {
		if !validName.MatchString(name) {
			return fmt.Errorf("resource name %q must start with a lowercase letter "+
				"and contain up to 40 lowercase letters, digits or hyphens",
				name)
		}

		definition := c[name]
		if err := validateDefinition(name, definition); err != nil {
			return err
		}
	}

	counts := make(map[string]int)
	for _, name := range slices.Sorted(maps.Keys(c)) {
		definition := c[name]
		if definition.Existing {
			continue
		}
		counts[definition.Type]++
		if definition.Type != ContainerApp &&
			definition.Type != AppService &&
			definition.Type != OpenAIModel &&
			counts[definition.Type] > 1 {
			return fmt.Errorf("the generator supports at most one %s resource per infrastructure entry",
				definition.Type)
		}
	}

	if err := validateLinks(c); err != nil {
		return err
	}
	if err := validateGeneratedHostEnvNames(c); err != nil {
		return err
	}
	hasDeployableKeyVault := false
	for _, definition := range c {
		if definition.Type == KeyVault && !definition.Existing {
			hasDeployableKeyVault = true
			break
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c)) {
		d := c[name]
		if !d.Existing && (d.Type == Mongo || d.Type == Redis) {
			if !hasDeployableKeyVault {
				if keyVault, exists := c["vault"]; exists && keyVault.Type != KeyVault {
					return fmt.Errorf("resource %q requires a deployable keyvault resource named vault", name)
				}
				for keyVaultName, keyVault := range c {
					if keyVault.Type == KeyVault && keyVault.Existing {
						return fmt.Errorf("resource %q requires a deployable keyvault, but %q is existing",
							name, keyVaultName)
					}
				}
			}
		}
	}
	return validateGeneratedNames(c)
}

func validateDefinition(name string, d Definition) error {
	switch d.Type {
	case Redis, Mongo, Cosmos, ContainerApp, AppService, OpenAIModel,
		EventHubs, ServiceBus, Storage, AIProject, AISearch, KeyVault:
	default:
		return fmt.Errorf("resource %q has unsupported type %q", name, d.Type)
	}

	if d.Existing {
		if d.Type == ContainerApp || d.Type == AppService {
			return fmt.Errorf("host resource %q cannot be marked existing", name)
		}
		if !existingResourceTypes[d.Type] {
			return fmt.Errorf("resource %q of type %q cannot be marked existing", name, d.Type)
		}
		if d.Port != 0 || len(d.Env) > 0 || d.Runtime != (AppServiceRuntime{}) ||
			d.StartupCommand != "" || d.Model != (AIModelPropsModel{}) ||
			len(d.Containers) > 0 || len(d.StorageContainers) > 0 ||
			len(d.Queues) > 0 || len(d.Topics) > 0 ||
			len(d.Hubs) > 0 || len(d.Models) > 0 || len(d.Uses) > 0 {
			return fmt.Errorf("existing resource %q cannot also specify deployment properties", name)
		}
		return nil
	}
	if d.Type != ContainerApp && d.Type != AppService && len(d.Uses) > 0 {
		return fmt.Errorf("resource %q of type %q cannot declare uses; only hosts support links", name, d.Type)
	}

	switch d.Type {
	case ContainerApp:
		if d.Port < 1 || d.Port > 65535 {
			return fmt.Errorf("container app %q requires a port between 1 and 65535", name)
		}
		if d.Runtime != (AppServiceRuntime{}) || d.StartupCommand != "" ||
			d.Model != (AIModelPropsModel{}) || len(d.Containers) > 0 ||
			len(d.Queues) > 0 || len(d.Topics) > 0 || len(d.Hubs) > 0 || len(d.Models) > 0 {
			return invalidProperties(name, d.Type)
		}
		if err := validateEnv(name, d.Env); err != nil {
			return err
		}
	case AppService:
		if d.Port < 1 || d.Port > 65535 {
			return fmt.Errorf("App Service %q requires a port between 1 and 65535", name)
		}
		if d.Runtime.Stack != "python" && d.Runtime.Stack != "node" {
			return fmt.Errorf("App Service %q runtime.stack must be python or node", name)
		}
		if d.Runtime.Version == "" {
			return fmt.Errorf("App Service %q runtime.version is required", name)
		}
		if d.Model != (AIModelPropsModel{}) || len(d.Containers) > 0 ||
			len(d.Queues) > 0 || len(d.Topics) > 0 || len(d.Hubs) > 0 || len(d.Models) > 0 {
			return invalidProperties(name, d.Type)
		}
		if err := validateEnv(name, d.Env); err != nil {
			return err
		}
	case OpenAIModel:
		if d.Model.Name == "" || d.Model.Version == "" {
			return fmt.Errorf("OpenAI model %q requires model.name and model.version", name)
		}
		if d.Port != 0 || len(d.Env) > 0 || d.Runtime != (AppServiceRuntime{}) ||
			d.StartupCommand != "" || len(d.Containers) > 0 || len(d.Queues) > 0 ||
			len(d.Topics) > 0 || len(d.Hubs) > 0 || len(d.Models) > 0 {
			return invalidProperties(name, d.Type)
		}
	case Cosmos:
		if d.Port != 0 || len(d.Env) > 0 || d.Runtime != (AppServiceRuntime{}) ||
			d.StartupCommand != "" || d.Model != (AIModelPropsModel{}) ||
			len(d.StorageContainers) > 0 || len(d.Queues) > 0 || len(d.Topics) > 0 ||
			len(d.Hubs) > 0 || len(d.Models) > 0 {
			return invalidProperties(name, d.Type)
		}
		for i, container := range d.Containers {
			if container.Name == "" {
				return fmt.Errorf("Cosmos DB resource %q container %d requires a name", name, i)
			}
		}
	case EventHubs:
		if d.Port != 0 || len(d.Env) > 0 || d.Runtime != (AppServiceRuntime{}) ||
			d.StartupCommand != "" || d.Model != (AIModelPropsModel{}) ||
			len(d.Containers) > 0 || len(d.Queues) > 0 || len(d.Topics) > 0 || len(d.Models) > 0 {
			return invalidProperties(name, d.Type)
		}
	case ServiceBus:
		if d.Port != 0 || len(d.Env) > 0 || d.Runtime != (AppServiceRuntime{}) ||
			d.StartupCommand != "" || d.Model != (AIModelPropsModel{}) ||
			len(d.Containers) > 0 || len(d.Hubs) > 0 || len(d.Models) > 0 {
			return invalidProperties(name, d.Type)
		}
	case Storage:
		if d.Port != 0 || len(d.Env) > 0 || d.Runtime != (AppServiceRuntime{}) ||
			d.StartupCommand != "" || d.Model != (AIModelPropsModel{}) ||
			len(d.Containers) > 0 || len(d.Queues) > 0 || len(d.Topics) > 0 ||
			len(d.Hubs) > 0 || len(d.Models) > 0 {
			return invalidProperties(name, d.Type)
		}
	case AIProject:
		if d.Port != 0 || len(d.Env) > 0 || d.Runtime != (AppServiceRuntime{}) ||
			d.StartupCommand != "" || d.Model != (AIModelPropsModel{}) ||
			len(d.Containers) > 0 || len(d.Queues) > 0 || len(d.Topics) > 0 || len(d.Hubs) > 0 {
			return invalidProperties(name, d.Type)
		}
		deployments := map[string]string{}
		for _, model := range d.Models {
			deployment := strings.ToLower(scaffold.BicepName(model.Name))
			if deployment == "" {
				return fmt.Errorf("AI model %q in resource %q requires a nonempty deployment name", model.Name, name)
			}
			if previous, exists := deployments[deployment]; exists {
				return fmt.Errorf("AI models %q and %q in resource %q produce the same deployment name",
					previous, model.Name, name)
			}
			deployments[deployment] = model.Name
		}
	case Redis, Mongo, AISearch, KeyVault:
		if d.Port != 0 || len(d.Env) > 0 || d.Runtime != (AppServiceRuntime{}) ||
			d.StartupCommand != "" || d.Model != (AIModelPropsModel{}) ||
			len(d.Containers) > 0 || len(d.StorageContainers) > 0 ||
			len(d.Queues) > 0 || len(d.Topics) > 0 ||
			len(d.Hubs) > 0 || len(d.Models) > 0 {
			return invalidProperties(name, d.Type)
		}
	}

	return nil
}

func invalidProperties(name, resourceType string) error {
	return fmt.Errorf("resource %q of type %q has properties that are not supported for that type", name, resourceType)
}

func validateEnv(name string, env []ServiceEnvVar) error {
	seen := make(map[string]struct{}, len(env))
	for _, v := range env {
		if !validEnvName.MatchString(v.Name) {
			return fmt.Errorf("host %q has invalid environment variable name %q", name, v.Name)
		}
		if (v.Value == "") == (v.Secret == "") {
			return fmt.Errorf("environment variable %q for host %q must set exactly one of value or secret",
				v.Name, name)
		}
		source := v.Value
		if v.Secret != "" {
			source = v.Secret
			substitutions, err := envSubstitutions(source)
			if err != nil {
				return fmt.Errorf("secret environment variable %q for host %q: %w", v.Name, name, err)
			}
			if len(substitutions) == 0 {
				return fmt.Errorf("secret environment variable %q for host %q must use an environment substitution",
					v.Name, name)
			}
		} else if _, err := envSubstitutions(source); err != nil {
			return fmt.Errorf("environment variable %q for host %q: %w", v.Name, name, err)
		}
		key := strings.ToUpper(v.Name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("host %q defines environment variable %q more than once", name, v.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateGeneratedHostEnvNames(config Config) error {
	for _, name := range slices.Sorted(maps.Keys(config)) {
		definition := config[name]
		if definition.Existing || (definition.Type != ContainerApp && definition.Type != AppService) {
			continue
		}

		generated := map[string]string{
			"AZURE_CLIENT_ID": "the host identity",
			"PORT":            "the host port",
		}
		addGenerated := func(key string, source string) {
			generated[strings.ToUpper(key)] = source
		}
		for _, dependency := range definition.Uses {
			used := config[dependency]
			if used.Existing {
				meta, ok := scaffold.ResourceMetaFromType(azureResourceType(used.Type))
				if ok {
					bindings, err := meta.BicepBindings("existing" + scaffold.BicepNameInfix(dependency))
					if err != nil {
						return fmt.Errorf("generating bindings for existing resource %q: %w", dependency, err)
					}
					for variable := range bindings {
						key := scaffold.EnvVarName(
							fmt.Sprintf("%s_%s", meta.StandardVarPrefix, ServiceKey(dependency)), variable)
						addGenerated(key, fmt.Sprintf("existing resource %q", dependency))
					}
				}
				continue
			}

			switch used.Type {
			case Mongo:
				addGenerated("MONGODB_URL", "MongoDB link")
			case Cosmos:
				addGenerated("AZURE_COSMOS_ENDPOINT", "Cosmos DB link")
			case Redis:
				for _, key := range []string{
					"REDIS_HOST", "REDIS_PORT", "REDIS_ENDPOINT", "REDIS_URL", "REDIS_PASSWORD",
				} {
					addGenerated(key, "Redis link")
				}
			case OpenAIModel:
				addGenerated("AZURE_OPENAI_ENDPOINT", "OpenAI model link")
			case EventHubs:
				addGenerated("AZURE_EVENT_HUBS_NAME", "Event Hubs link")
				addGenerated("AZURE_EVENT_HUBS_HOST", "Event Hubs link")
			case ServiceBus:
				addGenerated("AZURE_SERVICE_BUS_NAME", "Service Bus link")
				addGenerated("AZURE_SERVICE_BUS_HOST", "Service Bus link")
			case Storage:
				addGenerated("AZURE_STORAGE_ACCOUNT_NAME", "storage link")
				addGenerated("AZURE_STORAGE_BLOB_ENDPOINT", "storage link")
			case AIProject:
				addGenerated("AZURE_AI_PROJECT_ENDPOINT", "AI project link")
			case AISearch:
				addGenerated("AZURE_AI_SEARCH_ENDPOINT", "AI Search link")
			}
			if used.Type == ContainerApp || used.Type == AppService {
				addGenerated(ServiceKey(dependency)+"_BASE_URL", fmt.Sprintf("host link %q", dependency))
			}
			if used.Type == KeyVault {
				addGenerated("AZURE_KEY_VAULT_NAME", "Key Vault link")
				addGenerated("AZURE_KEY_VAULT_ENDPOINT", "Key Vault link")
			}
		}

		for _, variable := range definition.Env {
			if source, exists := generated[strings.ToUpper(variable.Name)]; exists {
				return fmt.Errorf("environment variable %q for host %q conflicts with %s",
					variable.Name, name, source)
			}
		}
	}
	return nil
}

func validateLinks(c Config) error {
	for _, name := range slices.Sorted(maps.Keys(c)) {
		d := c[name]
		seen := make(map[string]struct{}, len(d.Uses))
		for _, dependency := range d.Uses {
			if dependency == name {
				return fmt.Errorf("resource %q cannot use itself", name)
			}
			if _, ok := seen[dependency]; ok {
				return fmt.Errorf("resource %q uses %q more than once", name, dependency)
			}
			seen[dependency] = struct{}{}

			used, ok := c[dependency]
			if !ok {
				return fmt.Errorf("resource %q uses %q, which does not exist in the same entry", name, dependency)
			}
			if !supportedLink(d.Type, used.Type) {
				return fmt.Errorf("resource %q of type %q cannot use resource %q of type %q",
					name, d.Type, dependency, used.Type)
			}
			if used.Existing {
				switch used.Type {
				case Redis, Mongo:
					return fmt.Errorf("resource %q cannot use existing resource %q of type %q: "+
						"generated database credentials and connection bindings are not supported",
						name, dependency, used.Type)
				}
			}
		}
	}
	return nil
}

func supportedLink(hostType, targetType string) bool {
	switch targetType {
	case Redis, Mongo, Cosmos, OpenAIModel, EventHubs,
		ServiceBus, Storage, AIProject, AISearch, KeyVault:
		return hostType == ContainerApp || hostType == AppService
	case ContainerApp, AppService:
		return hostType == ContainerApp || hostType == AppService
	default:
		return false
	}
}

func validateGeneratedNames(c Config) error {
	reserved := map[string]struct{}{"abbrs": {}, "resourceToken": {}}
	hasServices, hasACA, hasAppService := false, false, false
	for _, definition := range c {
		if definition.Existing {
			continue
		}
		switch definition.Type {
		case ContainerApp:
			hasServices, hasACA = true, true
		case AppService:
			hasServices, hasAppService = true, true
		case Mongo:
			reserved["cosmosMongo"] = struct{}{}
		case Cosmos:
			reserved["cosmos"] = struct{}{}
		case Redis:
			reserved["redis"] = struct{}{}
		case Storage:
			reserved["storageAccountName"] = struct{}{}
			reserved["storageAccount"] = struct{}{}
		case OpenAIModel:
			reserved["accountName"] = struct{}{}
			reserved["account"] = struct{}{}
			reserved["localUserOpenAIIdentity"] = struct{}{}
		case EventHubs:
			reserved["eventHubNamespace"] = struct{}{}
		case ServiceBus:
			reserved["serviceBusNamespace"] = struct{}{}
		case AISearch:
			reserved["search"] = struct{}{}
		case KeyVault:
			reserved["keyVault"] = struct{}{}
		}
	}
	for _, definition := range c {
		if definition.Type == Mongo || definition.Type == Redis {
			reserved["keyVault"] = struct{}{}
		}
	}
	if hasServices {
		reserved["monitoring"] = struct{}{}
	}
	if hasACA {
		reserved["containerRegistry"] = struct{}{}
		reserved["containerAppsEnvironment"] = struct{}{}
	}
	if hasAppService {
		reserved["appServicePlan"] = struct{}{}
	}
	outputs := make(map[string]string, len(c)*3)
	registerOutput := func(output string, resource string) error {
		if prior, exists := outputs[output]; exists {
			return fmt.Errorf("resources %q and %q produce the same output name %q", prior, resource, output)
		}
		outputs[output] = resource
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(c)) {
		definition := c[name]
		if definition.Existing {
			continue
		}
		output := "AZURE_RESOURCE_" + scaffold.AlphaSnakeUpper(name) + "_ID"
		if special := reservedOutput(definition.Type); special != "" {
			output = special
		}
		if err := registerOutput(output, name); err != nil {
			return err
		}
		if definition.Type == ContainerApp || definition.Type == AppService {
			serviceKey := scaffold.AlphaSnakeUpper(name)
			for _, suffix := range []string{"RESOURCE_GROUP", "NAME"} {
				if err := registerOutput("SERVICE_"+serviceKey+"_"+suffix, name); err != nil {
					return err
				}
			}
			if definition.Type == ContainerApp {
				if err := registerOutput("SERVICE_"+serviceKey+"_REGISTRY_ENDPOINT", name); err != nil {
					return err
				}
			}
		}
	}

	symbols := make(map[string]string, len(c)*2)
	for symbol := range reserved {
		symbols[strings.ToLower(symbol)] = "scaffold"
	}
	containerAppNames := make(map[string]string)
	for _, name := range slices.Sorted(maps.Keys(c)) {
		definition := c[name]
		var symbolsToRegister []string
		if !definition.Existing && (definition.Type == ContainerApp || definition.Type == AppService) {
			hostName := scaffold.BicepName(name)
			symbolsToRegister = append(symbolsToRegister, hostName, hostName+"Identity")
			usesOpenAIModel, usesAIProject := false, false
			if definition.Type == ContainerApp {
				symbolsToRegister = append(symbolsToRegister, hostName+"FetchLatestImage")
			}
			for _, used := range definition.Uses {
				if c[used].Type == OpenAIModel {
					usesOpenAIModel = true
				}
				if c[used].Type == AIProject {
					usesAIProject = true
				}
			}
			if usesOpenAIModel {
				symbolsToRegister = append(symbolsToRegister, hostName+"OpenAIIdentity")
			}
			if usesAIProject {
				symbolsToRegister = append(symbolsToRegister,
					hostName+"backendRoleAzureAIDeveloperRG",
					hostName+"backendRoleCognitiveServicesUserRG")
			}
		}
		if definition.Existing {
			existing := "existing" + scaffold.BicepNameInfix(name)
			symbolsToRegister = append(symbolsToRegister, existing, existing+"IdSegments")
		}
		for _, symbol := range symbolsToRegister {
			key := strings.ToLower(symbol)
			if prior, exists := symbols[key]; exists {
				return fmt.Errorf("resource names %q and %q produce the same Bicep identifier %q",
					prior, name, symbol)
			}
			symbols[key] = name
		}
		if !definition.Existing && definition.Type == ContainerApp {
			azureName := scaffold.ContainerAppName(name)
			if prior, exists := containerAppNames[azureName]; exists {
				return fmt.Errorf("container app names %q and %q produce the same Azure name %q",
					prior, name, azureName)
			}
			containerAppNames[azureName] = name
		}

	}
	return nil
}

type envSubstitution struct {
	start, end int
	name       string
}

func envSubstitutions(value string) ([]envSubstitution, error) {
	var substitutions []envSubstitution
	for offset := 0; offset < len(value); {
		start := strings.Index(value[offset:], "${")
		if start < 0 {
			break
		}
		start += offset
		end := strings.IndexByte(value[start+2:], '}')
		if end < 0 {
			return nil, fmt.Errorf("unterminated environment substitution")
		}
		end += start + 2
		raw := value[start+2 : end]
		name := raw
		if before, _, ok := strings.Cut(raw, ":="); ok {
			name = before
		}
		if !validEnvName.MatchString(name) {
			return nil, fmt.Errorf("invalid environment substitution %q", raw)
		}
		substitutions = append(substitutions, envSubstitution{start: start, end: end + 1, name: name})
		offset = end + 1
	}
	return substitutions, nil
}

func reservedOutput(resourceType string) string {
	switch resourceType {
	case Redis:
		return "AZURE_RESOURCE_REDIS_ID"
	case Storage:
		return "AZURE_RESOURCE_STORAGE_ID"
	case EventHubs:
		return "AZURE_RESOURCE_EVENT_HUBS_ID"
	case ServiceBus:
		return "AZURE_RESOURCE_SERVICE_BUS_ID"
	case AIProject:
		return "AZURE_RESOURCE_AI_PROJECT_ID"
	case AISearch:
		return "AZURE_RESOURCE_SEARCH_ID"
	case KeyVault:
		return "AZURE_RESOURCE_VAULT_ID"
	default:
		return ""
	}
}

var existingResourceTypes = map[string]bool{
	Redis: true, Mongo: true, Cosmos: true,
	OpenAIModel: true, EventHubs: true,
	ServiceBus: true, Storage: true, AIProject: true, AISearch: true, KeyVault: true,
}

// Raw returns the JSON-compatible representation sent to the project editing API.
func (c Config) Raw() (map[string]any, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ServiceKey returns the environment-key component used by azd service outputs.
func ServiceKey(name string) string {
	return scaffold.AlphaSnakeUpper(name)
}
