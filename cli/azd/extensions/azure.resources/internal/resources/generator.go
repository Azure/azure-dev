// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"azure.resources/internal/resources/scaffold"
)

type resourceConfig struct {
	Name string
	Definition
}

func generateInfraSpec(config Config) (*scaffold.InfraSpec, error) {
	spec := &scaffold.InfraSpec{}
	existing := make(map[string]*scaffold.ExistingResource)
	backends := make(map[string][]scaffold.ServiceReference)
	resources := make(map[string]*resourceConfig, len(config)+1)
	for name, definition := range config {
		resources[name] = &resourceConfig{Name: name, Definition: definition}
	}

	hasKeyVault := false
	for _, resource := range resources {
		if resource.Type == KeyVault {
			hasKeyVault = true
			break
		}
	}
	for _, name := range slices.Sorted(maps.Keys(resources)) {
		resource := resources[name]
		if dependent := DependentResourcesOf(resource); dependent != nil && !hasKeyVault {
			resources[dependent.Name] = dependent
			hasKeyVault = true
		}
		if !resource.Existing {
			continue
		}

		resourceMeta, ok := scaffold.ResourceMetaFromType(azureResourceType(resource.Type))
		if !ok {
			return nil, fmt.Errorf("resource type %q is not supported for existing resources", resource.Type)
		}
		declaration := scaffold.ExistingResource{
			Name:             "existing" + scaffold.BicepNameInfix(resource.Name),
			ApiVersion:       resourceMeta.ApiVersion,
			ResourceIdEnvVar: resourceIDName(resource.Name),
			ResourceType:     resourceMeta.ResourceType,
			RoleAssignments:  resourceMeta.RoleAssignments.Write,
		}
		if resourceMeta.ParentResourceType != "" {
			declaration.ResourceType = resourceMeta.ParentResourceType
		}
		if resource.Type == KeyVault {
			declaration.RoleAssignments = resourceMeta.RoleAssignments.Read
		}
		spec.Existing = append(spec.Existing, declaration)
		existing[name] = &declaration
	}

	for _, name := range slices.Sorted(maps.Keys(resources)) {
		resource := resources[name]
		if resource.Existing {
			continue
		}

		switch resource.Type {
		case Redis:
			spec.DbRedis = &scaffold.DatabaseRedis{}
		case Mongo:
			spec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{DatabaseName: resource.Name}
		case Cosmos:
			containers := make([]scaffold.CosmosSqlDatabaseContainer, 0, len(resource.Containers))
			for _, container := range resource.Containers {
				containers = append(containers, scaffold.CosmosSqlDatabaseContainer{
					ContainerName:     container.Name,
					PartitionKeyPaths: slices.Clone(container.PartitionKeys),
				})
			}
			spec.DbCosmos = &scaffold.DatabaseCosmos{
				DatabaseName: resource.Name,
				Containers:   containers,
			}
		case AppService:
			service := scaffold.ServiceSpec{
				Name:      resource.Name,
				Port:      -1,
				Env:       make(map[string]string),
				SecretEnv: make(map[string]string),
				Host:      scaffold.AppServiceKind,
			}
			if err := mapAppService(resource, &service, spec); err != nil {
				return nil, err
			}
			if err := mapHostUses(resource, &service, backends, existing, resources); err != nil {
				return nil, err
			}
			spec.Services = append(spec.Services, service)
		case ContainerApp:
			service := scaffold.ServiceSpec{
				Name:      resource.Name,
				Port:      -1,
				Env:       make(map[string]string),
				SecretEnv: make(map[string]string),
				Host:      scaffold.ContainerAppKind,
			}
			if err := mapHostProps(resource, &service, spec, resource.Port, resource.Env); err != nil {
				return nil, err
			}
			if err := mapHostUses(resource, &service, backends, existing, resources); err != nil {
				return nil, err
			}
			spec.Services = append(spec.Services, service)
		case OpenAIModel:
			spec.AIModels = append(spec.AIModels, scaffold.AIModel{
				Name: resource.Name,
				Model: scaffold.AIModelModel{
					Name:    resource.Model.Name,
					Version: resource.Model.Version,
				},
			})
		case EventHubs:
			spec.EventHubs = &scaffold.EventHubs{Hubs: slices.Clone(resource.Hubs)}
		case ServiceBus:
			spec.ServiceBus = &scaffold.ServiceBus{
				Queues: slices.Clone(resource.Queues),
				Topics: slices.Clone(resource.Topics),
			}
		case Storage:
			spec.StorageAccount = &scaffold.StorageAccount{Containers: slices.Clone(resource.StorageContainers)}
		case AIProject:
			models := make([]scaffold.AiFoundryModel, 0, len(resource.Models))
			for _, model := range resource.Models {
				models = append(models, scaffold.AiFoundryModel{
					AIModelModel: scaffold.AIModelModel{Name: model.Name, Version: model.Version},
					Format:       model.Format,
					Sku: scaffold.AiFoundryModelSku{
						Name:      model.Sku.Name,
						UsageName: model.Sku.UsageName,
						Capacity:  model.Sku.Capacity,
					},
				})
			}
			spec.AiFoundryProject = &scaffold.AiFoundrySpec{Name: resource.Name, Models: models}
		case AISearch:
			spec.AISearch = &scaffold.AISearch{}
		case KeyVault:
			spec.KeyVault = &scaffold.KeyVault{}
		}
	}

	for i := range spec.Services {
		service := &spec.Services[i]
		if frontends, ok := backends[service.Name]; ok {
			if service.Backend == nil {
				service.Backend = &scaffold.Backend{}
			}
			service.Backend.Frontends = append(service.Backend.Frontends, frontends...)
		}
	}
	slices.SortFunc(spec.Services, func(a, b scaffold.ServiceSpec) int {
		return strings.Compare(a.Name, b.Name)
	})
	return spec, nil
}

func DependentResourcesOf(resource *resourceConfig) *resourceConfig {
	switch resource.Type {
	case Mongo, Redis:
		return &resourceConfig{Name: "vault", Definition: Definition{Type: KeyVault}}
	default:
		return nil
	}
}

func azureResourceType(resourceType string) string {
	switch resourceType {
	case Redis:
		return "Microsoft.Cache/redis"
	case Mongo:
		return "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases"
	case Cosmos:
		return "Microsoft.DocumentDB/databaseAccounts/sqlDatabases"
	case ContainerApp:
		return "Microsoft.App/containerApps"
	case AppService:
		return "Microsoft.Web/sites"
	case OpenAIModel:
		return "Microsoft.CognitiveServices/accounts/deployments"
	case EventHubs:
		return "Microsoft.EventHub/namespaces"
	case ServiceBus:
		return "Microsoft.ServiceBus/namespaces"
	case Storage:
		return "Microsoft.Storage/storageAccounts"
	case AIProject:
		return "Microsoft.CognitiveServices/accounts/projects"
	case AISearch:
		return "Microsoft.Search/searchServices"
	case KeyVault:
		return "Microsoft.KeyVault/vaults"
	default:
		return ""
	}
}

func resourceIDName(name string) string {
	return "AZURE_RESOURCE_" + scaffold.AlphaSnakeUpper(name) + "_ID"
}

func mapAppService(resource *resourceConfig, service *scaffold.ServiceSpec, spec *scaffold.InfraSpec) error {
	service.Runtime = &scaffold.RuntimeInfo{
		Type:    resource.Runtime.Stack,
		Version: resource.Runtime.Version,
	}
	service.StartupCommand = resource.StartupCommand
	defaults := map[string]string{
		"SCM_DO_BUILD_DURING_DEPLOYMENT": "true",
		"ENABLE_ORYX_BUILD":              "true",
	}
	if resource.Runtime.Stack == "python" {
		defaults["PYTHON_ENABLE_GUNICORN_MULTIWORKERS"] = "true"
	}
	return mapHostProps(resource, service, spec, resource.Port,
		mergeDefaultEnvVars(defaults, resource.Env))
}

func mergeDefaultEnvVars(defaults map[string]string, user []ServiceEnvVar) []ServiceEnvVar {
	userNames := make(map[string]struct{}, len(user))
	for _, value := range user {
		userNames[value.Name] = struct{}{}
	}
	combined := make([]ServiceEnvVar, 0, len(defaults)+len(user))
	for _, name := range slices.Sorted(maps.Keys(defaults)) {
		if _, overridden := userNames[name]; !overridden {
			combined = append(combined, ServiceEnvVar{Name: name, Value: defaults[name]})
		}
	}
	return append(combined, user...)
}

func mapHostProps(
	resource *resourceConfig,
	service *scaffold.ServiceSpec,
	spec *scaffold.InfraSpec,
	port int,
	environment []ServiceEnvVar,
) error {
	for _, variable := range environment {
		value := variable.Value
		secret := variable.Secret != ""
		if secret {
			value = variable.Secret
		}
		bicepValue, err := genBicepParamsFromEnvSubst(value, secret, spec)
		if err != nil {
			return fmt.Errorf("environment variable %q for host %q: %w", variable.Name, resource.Name, err)
		}
		if secret {
			service.SecretEnv[variable.Name] = bicepValue
		} else {
			service.Env[variable.Name] = bicepValue
		}
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("port value %d for host %q must be between 1 and 65535", port, resource.Name)
	}
	service.Port = port
	return nil
}

func mapHostUses(
	resource *resourceConfig,
	service *scaffold.ServiceSpec,
	backends map[string][]scaffold.ServiceReference,
	existing map[string]*scaffold.ExistingResource,
	resources map[string]*resourceConfig,
) error {
	for _, use := range resource.Uses {
		used := resources[use]
		if used == nil {
			return fmt.Errorf("resource %q uses %q, which does not exist", resource.Name, use)
		}
		if used.Existing {
			declaration := existing[use]
			if declaration == nil {
				return fmt.Errorf("existing resource %q has no generated declaration", use)
			}
			meta, ok := scaffold.ResourceMetaFromType(azureResourceType(used.Type))
			if !ok {
				return fmt.Errorf("resource type %q is not supported for existing resources", used.Type)
			}
			results, err := meta.BicepBindings(declaration.Name)
			if err != nil {
				return fmt.Errorf("emitting bindings for existing resource %q: %w", use, err)
			}
			for key, value := range results {
				name := scaffold.EnvVarName(
					fmt.Sprintf("%s_%s", meta.StandardVarPrefix, ServiceKey(use)), key)
				if _, exists := service.Env[name]; exists {
					return fmt.Errorf("environment variable %q is defined more than once for host %q",
						name, resource.Name)
				}
				service.Env[name] = value
			}
			service.Existing = append(service.Existing, declaration)
			continue
		}

		switch used.Type {
		case Mongo:
			service.DbCosmosMongo = &scaffold.DatabaseReference{DatabaseName: used.Name}
		case Cosmos:
			service.DbCosmos = &scaffold.DatabaseReference{DatabaseName: used.Name}
		case Redis:
			service.DbRedis = &scaffold.DatabaseReference{DatabaseName: used.Name}
		case ContainerApp, AppService:
			if service.Frontend == nil {
				service.Frontend = &scaffold.Frontend{}
			}
			host := scaffold.ContainerAppKind
			if used.Type == AppService {
				host = scaffold.AppServiceKind
			}
			service.Frontend.Backends = append(service.Frontend.Backends,
				scaffold.ServiceReference{Name: use, Host: host})
			backends[use] = append(backends[use], scaffold.ServiceReference{
				Name: resource.Name, Host: service.Host,
			})
		case OpenAIModel:
			service.AIModels = append(service.AIModels, scaffold.AIModelReference{Name: use})
		case EventHubs:
			service.EventHubs = &scaffold.EventHubs{}
		case ServiceBus:
			service.ServiceBus = &scaffold.ServiceBus{}
		case Storage:
			service.StorageAccount = &scaffold.StorageReference{}
		case AIProject:
			service.AiFoundryProject = &scaffold.AiFoundrySpec{}
		case AISearch:
			service.AISearch = &scaffold.AISearchReference{}
		case KeyVault:
			service.KeyVault = &scaffold.KeyVaultReference{}
		default:
			return fmt.Errorf("resource %q of type %q cannot be linked from host %q", use, used.Type, resource.Name)
		}
	}
	return nil
}

func genBicepParamsFromEnvSubst(value string, secret bool, spec *scaffold.InfraSpec) (string, error) {
	variables, err := envSubstitutions(value)
	if err != nil {
		return "", err
	}
	if secret && len(variables) == 0 {
		return "", fmt.Errorf("secret values must be supplied through an environment substitution")
	}
	for _, variable := range variables {
		if err := setParameter(spec, hostEnvParameterName(variable.name),
			value[variable.start:variable.end], secret); err != nil {
			return "", err
		}
	}
	if len(variables) == 0 {
		return "'" + scaffold.BicepString(value) + "'", nil
	}
	if len(variables) == 1 && variables[0].start == 0 && variables[0].end == len(value) {
		return hostEnvParameterName(variables[0].name), nil
	}

	var result strings.Builder
	result.WriteByte('\'')
	previous := 0
	for _, variable := range variables {
		result.WriteString(scaffold.BicepString(value[previous:variable.start]))
		result.WriteString("${")
		result.WriteString(hostEnvParameterName(variable.name))
		result.WriteByte('}')
		previous = variable.end
	}
	result.WriteString(scaffold.BicepString(value[previous:]))
	result.WriteByte('\'')
	return result.String(), nil
}

// hostEnvParameterName returns the Bicep parameter name for a ${VAR} reference in a host resource's env setting.
func hostEnvParameterName(variable string) string {
	// The '_' distinguishes host-env parameters from identifiers generated for the templates.
	return "hostEnv_" + scaffold.BicepName(variable)
}

func setParameter(spec *scaffold.InfraSpec, name string, value string, secret bool) error {
	for i := range spec.Parameters {
		parameter := &spec.Parameters[i]
		if parameter.Name != name {
			continue
		}
		current, ok := parameter.Value.(string)
		if !ok || current != value {
			return fmt.Errorf("parameter name collision for %q", name)
		}
		parameter.Secret = parameter.Secret || secret
		return nil
	}
	spec.Parameters = append(spec.Parameters, scaffold.Parameter{
		Name: name, Value: value, Type: "string", Secret: secret,
	})
	return nil
}
