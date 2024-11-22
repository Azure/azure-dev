// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/psanford/memfs"
)

// Generates the in-memory contents of an `infra` directory.
func infraFs(_ context.Context, prjConfig *ProjectConfig,
	console *input.Console, context *context.Context) (fs.FS, error) {
	t, err := scaffold.Load()
	if err != nil {
		return nil, fmt.Errorf("loading scaffold templates: %w", err)
	}

	infraSpec, err := infraSpec(prjConfig, console, context)
	if err != nil {
		return nil, fmt.Errorf("generating infrastructure spec: %w", err)
	}

	files, err := scaffold.ExecInfraFs(t, *infraSpec)
	if err != nil {
		return nil, fmt.Errorf("executing scaffold templates: %w", err)
	}

	return files, nil
}

// Returns the infrastructure configuration that points to a temporary, generated `infra` directory on the filesystem.
func tempInfra(
	ctx context.Context,
	prjConfig *ProjectConfig,
	console *input.Console,
	context *context.Context) (*Infra, error) {
	tmpDir, err := os.MkdirTemp("", "azd-infra")
	if err != nil {
		return nil, fmt.Errorf("creating temporary directory: %w", err)
	}

	files, err := infraFs(ctx, prjConfig, console, context)
	if err != nil {
		return nil, err
	}

	err = fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		target := filepath.Join(tmpDir, path)
		if err := os.MkdirAll(filepath.Dir(target), osutil.PermissionDirectoryOwnerOnly); err != nil {
			return err
		}

		contents, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}

		return os.WriteFile(target, contents, d.Type().Perm())
	})
	if err != nil {
		return nil, fmt.Errorf("writing infrastructure: %w", err)
	}

	return &Infra{
		Options: provisioning.Options{
			Provider: provisioning.Bicep,
			Path:     tmpDir,
			Module:   DefaultModule,
		},
		cleanupDir: tmpDir,
	}, nil
}

// Generates the filesystem of all infrastructure files to be placed, rooted at the project directory.
// The content only includes `./infra` currently.
func infraFsForProject(ctx context.Context, prjConfig *ProjectConfig,
	console *input.Console, context *context.Context) (fs.FS, error) {
	infraFS, err := infraFs(ctx, prjConfig, console, context)
	if err != nil {
		return nil, err
	}

	infraPathPrefix := DefaultPath
	if prjConfig.Infra.Path != "" {
		infraPathPrefix = prjConfig.Infra.Path
	}

	// root the generated content at the project directory
	generatedFS := memfs.New()
	err = fs.WalkDir(infraFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		err = generatedFS.MkdirAll(filepath.Join(infraPathPrefix, filepath.Dir(path)), osutil.PermissionDirectoryOwnerOnly)
		if err != nil {
			return err
		}

		contents, err := fs.ReadFile(infraFS, path)
		if err != nil {
			return err
		}

		return generatedFS.WriteFile(filepath.Join(infraPathPrefix, path), contents, d.Type().Perm())
	})
	if err != nil {
		return nil, err
	}

	return generatedFS, nil
}

func infraSpec(projectConfig *ProjectConfig,
	console *input.Console, context *context.Context) (*scaffold.InfraSpec, error) {
	infraSpec := scaffold.InfraSpec{}
	for _, resource := range projectConfig.Resources {
		switch resource.Type {
		case ResourceTypeDbRedis:
			infraSpec.DbRedis = &scaffold.DatabaseRedis{}
		case ResourceTypeDbMongo:
			infraSpec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{
				DatabaseName: resource.Props.(MongoDBProps).DatabaseName,
			}
		case ResourceTypeDbPostgres:
			infraSpec.DbPostgres = &scaffold.DatabasePostgres{
				DatabaseName: resource.Props.(PostgresProps).DatabaseName,
				DatabaseUser: "pgadmin",
				AuthType:     resource.Props.(PostgresProps).AuthType,
			}
		case ResourceTypeDbMySQL:
			infraSpec.DbMySql = &scaffold.DatabaseMySql{
				DatabaseName: resource.Props.(MySQLProps).DatabaseName,
				DatabaseUser: "mysqladmin",
				AuthType:     resource.Props.(MySQLProps).AuthType,
			}
		case ResourceTypeDbCosmos:
			infraSpec.DbCosmos = &scaffold.DatabaseCosmosAccount{
				DatabaseName: resource.Props.(CosmosDBProps).DatabaseName,
			}
			containers := resource.Props.(CosmosDBProps).Containers
			for _, container := range containers {
				infraSpec.DbCosmos.Containers = append(infraSpec.DbCosmos.Containers, scaffold.CosmosSqlDatabaseContainer{
					ContainerName:     container.ContainerName,
					PartitionKeyPaths: container.PartitionKeyPaths,
				})
			}
		case ResourceTypeMessagingServiceBus:
			props := resource.Props.(ServiceBusProps)
			infraSpec.AzureServiceBus = &scaffold.AzureDepServiceBus{
				Queues:   props.Queues,
				AuthType: props.AuthType,
				IsJms:    props.IsJms,
			}
		case ResourceTypeMessagingEventHubs:
			props := resource.Props.(EventHubsProps)
			infraSpec.AzureEventHubs = &scaffold.AzureDepEventHubs{
				EventHubNames: props.EventHubNames,
				AuthType:      props.AuthType,
				UseKafka:      false,
			}
		case ResourceTypeMessagingKafka:
			props := resource.Props.(KafkaProps)
			infraSpec.AzureEventHubs = &scaffold.AzureDepEventHubs{
				EventHubNames:     props.Topics,
				AuthType:          props.AuthType,
				UseKafka:          true,
				SpringBootVersion: props.SpringBootVersion,
			}
		case ResourceTypeStorage:
			props := resource.Props.(StorageProps)
			infraSpec.AzureStorageAccount = &scaffold.AzureDepStorageAccount{
				ContainerNames: props.Containers,
				AuthType:       props.AuthType,
			}
		case ResourceTypeHostContainerApp:
			serviceSpec := scaffold.ServiceSpec{
				Name: resource.Name,
				Port: -1,
			}
			err := handleContainerAppProps(resource, &serviceSpec, &infraSpec)
			if err != nil {
				return nil, err
			}
			infraSpec.Services = append(infraSpec.Services, serviceSpec)
		case ResourceTypeOpenAiModel:
			props := resource.Props.(AIModelProps)
			if len(props.Model.Name) == 0 {
				return nil, fmt.Errorf("resources.%s.model is required", resource.Name)
			}

			if len(props.Model.Version) == 0 {
				return nil, fmt.Errorf("resources.%s.version is required", resource.Name)
			}

			infraSpec.AIModels = append(infraSpec.AIModels, scaffold.AIModel{
				Name: resource.Name,
				Model: scaffold.AIModelModel{
					Name:    props.Model.Name,
					Version: props.Model.Version,
				},
			})
		}
	}

	err := mapUses(&infraSpec, projectConfig)
	if err != nil {
		return nil, err
	}

	err = printHintsAboutUses(&infraSpec, projectConfig, console, context)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(infraSpec.Services, func(a, b scaffold.ServiceSpec) int {
		return strings.Compare(a.Name, b.Name)
	})

	return &infraSpec, nil
}

func mapUses(infraSpec *scaffold.InfraSpec, projectConfig *ProjectConfig) error {
	for i := range infraSpec.Services {
		userSpec := &infraSpec.Services[i]
		userResourceName := userSpec.Name
		userResource, ok := projectConfig.Resources[userResourceName]
		if !ok {
			return fmt.Errorf("service (%s) exist, but there isn't a resource with that name",
				userResourceName)
		}
		for _, usedResourceName := range userResource.Uses {
			usedResource, ok := projectConfig.Resources[usedResourceName]
			if !ok {
				return fmt.Errorf("in azure.yaml, (%s) uses (%s), but (%s) doesn't",
					userResourceName, usedResourceName, usedResourceName)
			}
			switch usedResource.Type {
			case ResourceTypeDbPostgres:
				userSpec.DbPostgres = infraSpec.DbPostgres
			case ResourceTypeDbMySQL:
				userSpec.DbMySql = infraSpec.DbMySql
			case ResourceTypeDbRedis:
				userSpec.DbRedis = infraSpec.DbRedis
			case ResourceTypeDbMongo:
				userSpec.DbCosmosMongo = infraSpec.DbCosmosMongo
			case ResourceTypeDbCosmos:
				userSpec.DbCosmos = infraSpec.DbCosmos
			case ResourceTypeMessagingServiceBus:
				userSpec.AzureServiceBus = infraSpec.AzureServiceBus
			case ResourceTypeMessagingEventHubs, ResourceTypeMessagingKafka:
				userSpec.AzureEventHubs = infraSpec.AzureEventHubs
			case ResourceTypeStorage:
				userSpec.AzureStorageAccount = infraSpec.AzureStorageAccount
			case ResourceTypeHostContainerApp:
				err := fulfillFrontendBackend(userSpec, usedResource, infraSpec)
				if err != nil {
					return err
				}
			case ResourceTypeOpenAiModel:
				userSpec.AIModels = append(userSpec.AIModels, scaffold.AIModelReference{Name: usedResource.Name})
			default:
				return fmt.Errorf("resource (%s) uses (%s), but the type of (%s) is (%s), which is unsupported",
					userResource.Name, usedResource.Name, usedResource.Name, usedResource.Type)
			}
		}
	}
	return nil
}

func printHintsAboutUses(infraSpec *scaffold.InfraSpec, projectConfig *ProjectConfig,
	console *input.Console,
	context *context.Context) error {
	for i := range infraSpec.Services {
		userSpec := &infraSpec.Services[i]
		userResourceName := userSpec.Name
		userResource, ok := projectConfig.Resources[userResourceName]
		if !ok {
			return fmt.Errorf("service (%s) exist, but there isn't a resource with that name",
				userResourceName)
		}
		for _, usedResourceName := range userResource.Uses {
			usedResource, ok := projectConfig.Resources[usedResourceName]
			if !ok {
				return fmt.Errorf("in azure.yaml, (%s) uses (%s), but (%s) doesn't",
					userResourceName, usedResourceName, usedResourceName)
			}
			if *console != nil {
				(*console).Message(*context, fmt.Sprintf("CAUTION: In azure.yaml, '%s' uses '%s'. "+
					"After deployed, the 'uses' is achieved by providing these environment variables: ",
					userResourceName, usedResourceName))
			}
			switch usedResource.Type {
			case ResourceTypeDbPostgres:
				err := printHintsAboutUsePostgres(userSpec.DbPostgres.AuthType, console, context)
				if err != nil {
					return err
				}
			case ResourceTypeDbMySQL:
				err := printHintsAboutUseMySql(userSpec.DbPostgres.AuthType, console, context)
				if err != nil {
					return err
				}
			case ResourceTypeDbRedis:
				printHintsAboutUseRedis(console, context)
			case ResourceTypeDbMongo:
				printHintsAboutUseMongo(console, context)
			case ResourceTypeDbCosmos:
				printHintsAboutUseCosmos(console, context)
			case ResourceTypeMessagingServiceBus:
				err := printHintsAboutUseServiceBus(userSpec.AzureServiceBus.IsJms,
					userSpec.AzureServiceBus.AuthType, console, context)
				if err != nil {
					return err
				}
			case ResourceTypeMessagingEventHubs, ResourceTypeMessagingKafka:
				err := printHintsAboutUseEventHubs(userSpec.AzureEventHubs.UseKafka,
					userSpec.AzureEventHubs.AuthType, userSpec.AzureEventHubs.SpringBootVersion, console, context)
				if err != nil {
					return err
				}
			case ResourceTypeStorage:
				err := printHintsAboutUseStorageAccount(userSpec.AzureStorageAccount.AuthType, console, context)
				if err != nil {
					return err
				}
			case ResourceTypeHostContainerApp:
				printHintsAboutUseHostContainerApp(userResourceName, usedResourceName, console, context)
			case ResourceTypeOpenAiModel:
				printHintsAboutUseOpenAiModel(console, context)
			default:
				return fmt.Errorf("resource (%s) uses (%s), but the type of (%s) is (%s), "+
					"which is doen't add necessary environment variable",
					userResource.Name, usedResource.Name, usedResource.Name, usedResource.Type)
			}
			if *console != nil {
				(*console).Message(*context, "Please make sure your application used the right environment variable name.\n")
			}

		}
	}
	return nil

}

func handleContainerAppProps(
	resourceConfig *ResourceConfig, serviceSpec *scaffold.ServiceSpec, infraSpec *scaffold.InfraSpec) error {
	props := resourceConfig.Props.(ContainerAppProps)
	for _, envVar := range props.Env {
		if len(envVar.Value) == 0 && len(envVar.Secret) == 0 {
			return fmt.Errorf(
				"environment variable %s for host %s is invalid: both value and secret are empty",
				envVar.Name,
				resourceConfig.Name)
		}

		if len(envVar.Value) > 0 && len(envVar.Secret) > 0 {
			return fmt.Errorf(
				"environment variable %s for host %s is invalid: both value and secret are set",
				envVar.Name,
				resourceConfig.Name)
		}

		isSecret := len(envVar.Secret) > 0
		value := envVar.Value
		if isSecret {
			value = envVar.Secret
		}

		// Notice that we derive isSecret from its usage.
		// This is generally correct, except for the case where:
		// - CONNECTION_STRING: ${DB_HOST}:${DB_SECRET}
		// Here, DB_HOST is not a secret, but DB_SECRET is. And yet, DB_HOST will be marked as a secret.
		// This is a limitation of the current implementation, but it's safer to mark both as secrets above.
		evaluatedValue := genBicepParamsFromEnvSubst(value, isSecret, infraSpec)
		serviceSpec.Env[envVar.Name] = evaluatedValue
	}

	port := props.Port
	if port < 1 || port > 65535 {
		return fmt.Errorf("port value %d for host %s must be between 1 and 65535", port, resourceConfig.Name)
	}

	serviceSpec.Port = port
	return nil
}

func setParameter(spec *scaffold.InfraSpec, name string, value string, isSecret bool) {
	for _, parameters := range spec.Parameters {
		if parameters.Name == name { // handle existing parameter
			if isSecret && !parameters.Secret {
				// escalate the parameter to a secret
				parameters.Secret = true
			}

			// prevent auto-generated parameters from being overwritten with different values
			if valStr, ok := parameters.Value.(string); !ok || ok && valStr != value {
				// if you are a maintainer and run into this error, consider using a different, unique name
				panic(fmt.Sprintf(
					"parameter collision: parameter %s already set to %s, cannot set to %s", name, parameters.Value, value))
			}

			return
		}
	}

	spec.Parameters = append(spec.Parameters, scaffold.Parameter{
		Name:   name,
		Value:  value,
		Type:   "string",
		Secret: isSecret,
	})
}

// genBicepParamsFromEnvSubst generates Bicep input parameters from a string containing envsubst expression(s),
// returning the substituted string that references these parameters.
//
// If the string is a literal, it is returned as is.
// If isSecret is true, the parameter is marked as a secret.
func genBicepParamsFromEnvSubst(
	s string,
	isSecret bool,
	infraSpec *scaffold.InfraSpec) string {
	names, locations := parseEnvSubstVariables(s)

	// add all expressions as parameters
	for i, name := range names {
		expression := s[locations[i].start : locations[i].stop+1]
		setParameter(infraSpec, scaffold.BicepName(name), expression, isSecret)
	}

	var result string
	if len(names) == 0 {
		// literal string with no expressions, quote the value as a Bicep string
		result = "'" + s + "'"
	} else if len(names) == 1 {
		// single expression, return the bicep parameter name to reference the expression
		result = scaffold.BicepName(names[0])
	} else {
		// multiple expressions
		// construct the string with all expressions replaced by parameter references as a Bicep interpolated string
		previous := 0
		result = "'"
		for i, loc := range locations {
			// replace each expression with references by variable name
			result += s[previous:loc.start]
			result += "${"
			result += scaffold.BicepName(names[i])
			result += "}"
			previous = loc.stop + 1
		}
		result += "'"
	}

	return result
}

func fulfillFrontendBackend(
	userSpec *scaffold.ServiceSpec, usedResource *ResourceConfig, infraSpec *scaffold.InfraSpec) error {
	if userSpec.Frontend == nil {
		userSpec.Frontend = &scaffold.Frontend{}
	}
	userSpec.Frontend.Backends =
		append(userSpec.Frontend.Backends, scaffold.ServiceReference{Name: usedResource.Name})

	usedSpec := getServiceSpecByName(infraSpec, usedResource.Name)
	if usedSpec == nil {
		return fmt.Errorf("'%s' uses '%s', but %s doesn't exist", userSpec.Name, usedResource.Name, usedResource.Name)
	}
	if usedSpec.Backend == nil {
		usedSpec.Backend = &scaffold.Backend{}
	}
	usedSpec.Backend.Frontends =
		append(usedSpec.Backend.Frontends, scaffold.ServiceReference{Name: userSpec.Name})
	return nil
}

func getServiceSpecByName(infraSpec *scaffold.InfraSpec, name string) *scaffold.ServiceSpec {
	for i := range infraSpec.Services {
		if infraSpec.Services[i].Name == name {
			return &infraSpec.Services[i]
		}
	}
	return nil
}

func printHintsAboutUsePostgres(authType internal.AuthType,
	console *input.Console, context *context.Context) error {
	if *console == nil {
		return nil
	}
	(*console).Message(*context, "POSTGRES_HOST=xxx")
	(*console).Message(*context, "POSTGRES_DATABASE=xxx")
	(*console).Message(*context, "POSTGRES_PORT=xxx")
	(*console).Message(*context, "spring.datasource.url=xxx")
	(*console).Message(*context, "spring.datasource.username=xxx")
	if authType == internal.AuthTypePassword {
		(*console).Message(*context, "POSTGRES_URL=xxx")
		(*console).Message(*context, "POSTGRES_USERNAME=xxx")
		(*console).Message(*context, "POSTGRES_PASSWORD=xxx")
		(*console).Message(*context, "spring.datasource.password=xxx")
	} else if authType == internal.AuthTypeUserAssignedManagedIdentity {
		(*console).Message(*context, "spring.datasource.azure.passwordless-enabled=true")
		(*console).Message(*context, "CAUTION: To make sure passwordless work well in your spring boot application, ")
		(*console).Message(*context, "make sure the following 2 things:")
		(*console).Message(*context, "1. Add required dependency: spring-cloud-azure-starter-jdbc-postgresql.")
		(*console).Message(*context, "2. Delete property 'spring.datasource.password' in your property file.")
		(*console).Message(*context, "Refs: https://learn.microsoft.com/en-us/azure/service-connector/")
		(*console).Message(*context, "how-to-integrate-mysql?tabs=springBoot#sample-code-1")
	} else {
		return fmt.Errorf("unsupported auth type for PostgreSQL. Supported types: %s, %s",
			internal.GetAuthTypeDescription(internal.AuthTypePassword),
			internal.GetAuthTypeDescription(internal.AuthTypeUserAssignedManagedIdentity))
	}
	return nil
}

func printHintsAboutUseMySql(authType internal.AuthType,
	console *input.Console, context *context.Context) error {
	if *console == nil {
		return nil
	}
	(*console).Message(*context, "MYSQL_HOST=xxx")
	(*console).Message(*context, "MYSQL_DATABASE=xxx")
	(*console).Message(*context, "MYSQL_PORT=xxx")
	(*console).Message(*context, "spring.datasource.url=xxx")
	(*console).Message(*context, "spring.datasource.username=xxx")
	if authType == internal.AuthTypePassword {
		(*console).Message(*context, "MYSQL_URL=xxx")
		(*console).Message(*context, "MYSQL_USERNAME=xxx")
		(*console).Message(*context, "MYSQL_PASSWORD=xxx")
		(*console).Message(*context, "spring.datasource.password=xxx")
	} else if authType == internal.AuthTypeUserAssignedManagedIdentity {
		(*console).Message(*context, "spring.datasource.azure.passwordless-enabled=true")
		(*console).Message(*context, "CAUTION: To make sure passwordless work well in your spring boot application, ")
		(*console).Message(*context, "Make sure the following 2 things:")
		(*console).Message(*context, "1. Add required dependency: spring-cloud-azure-starter-jdbc-postgresql.")
		(*console).Message(*context, "2. Delete property 'spring.datasource.password' in your property file.")
		(*console).Message(*context, "Refs: https://learn.microsoft.com/en-us/azure/service-connector/how-to-integrate-postgres?tabs=springBoot#sample-code-1")
	} else {
		return fmt.Errorf("unsupported auth type for MySql. Supported types are: %s, %s",
			internal.GetAuthTypeDescription(internal.AuthTypePassword),
			internal.GetAuthTypeDescription(internal.AuthTypeUserAssignedManagedIdentity))
	}
	return nil
}

func printHintsAboutUseRedis(console *input.Console, context *context.Context) {
	if *console == nil {
		return
	}
	(*console).Message(*context, "REDIS_HOST=xxx")
	(*console).Message(*context, "REDIS_PORT=xxx")
	(*console).Message(*context, "REDIS_URL=xxx")
	(*console).Message(*context, "REDIS_ENDPOINT=xxx")
	(*console).Message(*context, "REDIS_PASSWORD=xxx")
	(*console).Message(*context, "spring.data.redis.url=xxx")
}

func printHintsAboutUseMongo(console *input.Console, context *context.Context) {
	if *console == nil {
		return
	}
	(*console).Message(*context, "MONGODB_URL=xxx")
	(*console).Message(*context, "spring.data.mongodb.uri=xxx")
	(*console).Message(*context, "spring.data.mongodb.database=xxx")
}

func printHintsAboutUseCosmos(console *input.Console, context *context.Context) {
	if *console == nil {
		return
	}
	(*console).Message(*context, "spring.cloud.azure.cosmos.endpoint=xxx")
	(*console).Message(*context, "spring.cloud.azure.cosmos.database=xxx")
}

func printHintsAboutUseServiceBus(isJms bool, authType internal.AuthType,
	console *input.Console, context *context.Context) error {
	if *console == nil {
		return nil
	}
	if !isJms {
		(*console).Message(*context, "spring.cloud.azure.servicebus.namespace=xxx")
	}
	if authType == internal.AuthTypeUserAssignedManagedIdentity {
		(*console).Message(*context, "spring.cloud.azure.servicebus.connection-string=''")
		(*console).Message(*context, "spring.cloud.azure.servicebus.credential.managed-identity-enabled=true")
		(*console).Message(*context, "spring.cloud.azure.servicebus.credential.client-id=xxx")
	} else if authType == internal.AuthTypeConnectionString {
		(*console).Message(*context, "spring.cloud.azure.servicebus.connection-string=xxx")
		(*console).Message(*context, "spring.cloud.azure.servicebus.credential.managed-identity-enabled=false")
		(*console).Message(*context, "spring.cloud.azure.eventhubs.credential.client-id=xxx")
	} else {
		return fmt.Errorf("unsupported auth type for Service Bus. Supported types are: %s, %s",
			internal.GetAuthTypeDescription(internal.AuthTypeUserAssignedManagedIdentity),
			internal.GetAuthTypeDescription(internal.AuthTypeConnectionString))
	}
	return nil
}

func printHintsAboutUseEventHubs(UseKafka bool, authType internal.AuthType, springBootVersion string,
	console *input.Console, context *context.Context) error {
	if *console == nil {
		return nil
	}
	if !UseKafka {
		(*console).Message(*context, "spring.cloud.azure.eventhubs.namespace=xxx")
	} else {
		(*console).Message(*context, "spring.cloud.stream.kafka.binder.brokers=xxx")
		if strings.HasPrefix(springBootVersion, "2.") {
			(*console).Message(*context, "spring.cloud.stream.binders.kafka.environment.spring.main.sources=com.azure.spring.cloud.autoconfigure.eventhubs.kafka.AzureEventHubsKafkaAutoConfiguration")
		} else if strings.HasPrefix(springBootVersion, "3.") {
			(*console).Message(*context, "spring.cloud.stream.binders.kafka.environment.spring.main.sources=com.azure.spring.cloud.autoconfigure.implementation.eventhubs.kafka.AzureEventHubsKafkaAutoConfiguration")
		}
	}
	if authType == internal.AuthTypeUserAssignedManagedIdentity {
		(*console).Message(*context, "spring.cloud.azure.eventhubs.connection-string=''")
		(*console).Message(*context, "spring.cloud.azure.eventhubs.credential.managed-identity-enabled=true")
		(*console).Message(*context, "spring.cloud.azure.eventhubs.credential.client-id=xxx")
	} else if authType == internal.AuthTypeConnectionString {
		(*console).Message(*context, "spring.cloud.azure.eventhubs.connection-string=xxx")
		(*console).Message(*context, "spring.cloud.azure.eventhubs.credential.managed-identity-enabled=false")
		(*console).Message(*context, "spring.cloud.azure.eventhubs.credential.client-id=xxx")
	} else {
		return fmt.Errorf("unsupported auth type for Event Hubs. Supported types: %s, %s",
			internal.GetAuthTypeDescription(internal.AuthTypeUserAssignedManagedIdentity),
			internal.GetAuthTypeDescription(internal.AuthTypeConnectionString))
	}
	return nil
}

func printHintsAboutUseStorageAccount(authType internal.AuthType,
	console *input.Console, context *context.Context) error {
	if *console == nil {
		return nil
	}
	(*console).Message(*context, "spring.cloud.azure.eventhubs.processor.checkpoint-store.account-name=xxx")
	if authType == internal.AuthTypeUserAssignedManagedIdentity {
		(*console).Message(*context, "spring.cloud.azure.eventhubs.processor.checkpoint-store.connection-string=''")
		(*console).Message(*context, "spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.managed-identity-enabled=true")
		(*console).Message(*context, "spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.client-id=xxx")
	} else if authType == internal.AuthTypeConnectionString {
		(*console).Message(*context, "spring.cloud.azure.eventhubs.processor.checkpoint-store.connection-string=xxx")
		(*console).Message(*context, "spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.managed-identity-enabled=false")
		(*console).Message(*context, "spring.cloud.azure.eventhubs.processor.checkpoint-store.credential.client-id=xxx")
	} else {
		return fmt.Errorf("unsupported auth type for Storage Account. Supported types: %s, %s",
			internal.GetAuthTypeDescription(internal.AuthTypeUserAssignedManagedIdentity),
			internal.GetAuthTypeDescription(internal.AuthTypeConnectionString))
	}
	return nil
}

func printHintsAboutUseHostContainerApp(userResourceName string, usedResourceName string,
	console *input.Console, context *context.Context) {
	if *console == nil {
		return
	}
	(*console).Message(*context, fmt.Sprintf("Environemnt variables in %s:", userResourceName))
	(*console).Message(*context, fmt.Sprintf("%s_BASE_URL=xxx", strings.ToUpper(usedResourceName)))
	(*console).Message(*context, fmt.Sprintf("Environemnt variables in %s:", usedResourceName))
	(*console).Message(*context, fmt.Sprintf("%s_BASE_URL=xxx", strings.ToUpper(userResourceName)))
}

func printHintsAboutUseOpenAiModel(console *input.Console, context *context.Context) {
	if *console == nil {
		return
	}
	(*console).Message(*context, "AZURE_OPENAI_ENDPOINT")
}
