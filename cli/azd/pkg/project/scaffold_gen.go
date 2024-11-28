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
func infraFs(cxt context.Context, prjConfig *ProjectConfig, console input.Console) (fs.FS, error) {
	t, err := scaffold.Load()
	if err != nil {
		return nil, fmt.Errorf("loading scaffold templates: %w", err)
	}

	infraSpec, err := infraSpec(prjConfig, console, cxt)
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
	console input.Console) (*Infra, error) {
	tmpDir, err := os.MkdirTemp("", "azd-infra")
	if err != nil {
		return nil, fmt.Errorf("creating temporary directory: %w", err)
	}

	files, err := infraFs(ctx, prjConfig, console)
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
	console input.Console) (fs.FS, error) {
	infraFS, err := infraFs(ctx, prjConfig, console)
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
	console input.Console, ctx context.Context) (*scaffold.InfraSpec, error) {
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

	err = printEnvListAboutUses(&infraSpec, projectConfig, console, ctx)
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
				err := addUsageByEnv(infraSpec, userSpec, usedResource)
				if err != nil {
					return err
				}
			case ResourceTypeDbMySQL:
				userSpec.DbMySql = infraSpec.DbMySql
				err := addUsageByEnv(infraSpec, userSpec, usedResource)
				if err != nil {
					return err
				}
			case ResourceTypeDbRedis:
				userSpec.DbRedis = infraSpec.DbRedis
				err := addUsageByEnv(infraSpec, userSpec, usedResource)
				if err != nil {
					return err
				}
			case ResourceTypeDbMongo:
				userSpec.DbCosmosMongo = infraSpec.DbCosmosMongo
				err := addUsageByEnv(infraSpec, userSpec, usedResource)
				if err != nil {
					return err
				}
			case ResourceTypeDbCosmos:
				userSpec.DbCosmos = infraSpec.DbCosmos
				err := addUsageByEnv(infraSpec, userSpec, usedResource)
				if err != nil {
					return err
				}
			case ResourceTypeMessagingServiceBus:
				userSpec.AzureServiceBus = infraSpec.AzureServiceBus
				err := addUsageByEnv(infraSpec, userSpec, usedResource)
				if err != nil {
					return err
				}
			case ResourceTypeMessagingEventHubs, ResourceTypeMessagingKafka:
				userSpec.AzureEventHubs = infraSpec.AzureEventHubs
				err := addUsageByEnv(infraSpec, userSpec, usedResource)
				if err != nil {
					return err
				}
			case ResourceTypeStorage:
				userSpec.AzureStorageAccount = infraSpec.AzureStorageAccount
				err := addUsageByEnv(infraSpec, userSpec, usedResource)
				if err != nil {
					return err
				}
			case ResourceTypeOpenAiModel:
				userSpec.AIModels = append(userSpec.AIModels, scaffold.AIModelReference{Name: usedResource.Name})
				err := addUsageByEnv(infraSpec, userSpec, usedResource)
				if err != nil {
					return err
				}
			case ResourceTypeHostContainerApp:
				err := fulfillFrontendBackend(userSpec, usedResource, infraSpec)
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("resource (%s) uses (%s), but the type of (%s) is (%s), which is unsupported",
					userResource.Name, usedResource.Name, usedResource.Name, usedResource.Type)
			}
		}
	}
	return nil
}

func getAuthType(infraSpec *scaffold.InfraSpec, resourceType ResourceType) (internal.AuthType, error) {
	switch resourceType {
	case ResourceTypeDbPostgres:
		return infraSpec.DbPostgres.AuthType, nil
	case ResourceTypeDbMySQL:
		return infraSpec.DbMySql.AuthType, nil
	case ResourceTypeDbRedis:
		return internal.AuthTypePassword, nil
	case ResourceTypeDbMongo,
		ResourceTypeDbCosmos,
		ResourceTypeOpenAiModel,
		ResourceTypeHostContainerApp:
		return internal.AuthTypeUserAssignedManagedIdentity, nil
	case ResourceTypeMessagingServiceBus:
		return infraSpec.AzureServiceBus.AuthType, nil
	case ResourceTypeMessagingEventHubs, ResourceTypeMessagingKafka:
		return infraSpec.AzureEventHubs.AuthType, nil
	case ResourceTypeStorage:
		return infraSpec.AzureStorageAccount.AuthType, nil
	default:
		return internal.AuthTypeUnspecified, fmt.Errorf("can not get authType, resource type: %s", resourceType)
	}
}

func addUsageByEnv(infraSpec *scaffold.InfraSpec, userSpec *scaffold.ServiceSpec, usedResource *ResourceConfig) error {
	envs, err := getResourceConnectionEnvs(usedResource, infraSpec)
	if err != nil {
		return err
	}
	userSpec.Envs, err = mergeEnvWithDuplicationCheck(userSpec.Envs, envs)
	if err != nil {
		return err
	}
	return nil
}

func printEnvListAboutUses(infraSpec *scaffold.InfraSpec, projectConfig *ProjectConfig,
	console input.Console, ctx context.Context) error {
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
			console.Message(ctx, fmt.Sprintf("\nInformation about environment variables:\n"+
				"In azure.yaml, '%s' uses '%s'. \n"+
				"The 'uses' relashipship is implemented by environment variables. \n"+
				"Please make sure your application used the right environment variable. \n"+
				"Here is the list of environment variables: ",
				userResourceName, usedResourceName))
			switch usedResource.Type {
			case ResourceTypeDbPostgres, // do nothing. todo: add all other types
				ResourceTypeDbMySQL,
				ResourceTypeDbRedis,
				ResourceTypeDbMongo,
				ResourceTypeDbCosmos,
				ResourceTypeMessagingServiceBus,
				ResourceTypeMessagingEventHubs,
				ResourceTypeMessagingKafka,
				ResourceTypeStorage:
				variables, err := getResourceConnectionEnvs(usedResource, infraSpec)
				if err != nil {
					return err
				}
				for _, variable := range variables {
					console.Message(ctx, fmt.Sprintf("  %s=xxx", variable.Name))
				}
			case ResourceTypeHostContainerApp:
				printHintsAboutUseHostContainerApp(userResourceName, usedResourceName, console, ctx)
			default:
				return fmt.Errorf("resource (%s) uses (%s), but the type of (%s) is (%s), "+
					"which is doen't add necessary environment variable",
					userResource.Name, usedResource.Name, usedResource.Name, usedResource.Type)
			}
			console.Message(ctx, "\n")
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
		err := addNewEnvironmentVariable(serviceSpec, envVar.Name, evaluatedValue)
		if err != nil {
			return err
		}
		return nil
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
// The returned value is string, all expression inside are wrapped by "${}".
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
		// literal string with no expressions
		result = s
	} else if len(names) == 1 {
		// single expression, return the bicep parameter name to reference the expression
		result = "${" + scaffold.BicepName(names[0]) + "}"
	} else {
		// multiple expressions
		// construct the string with all expressions replaced by parameter references as a Bicep interpolated string
		previous := 0
		result = ""
		for i, loc := range locations {
			// replace each expression with references by variable name
			result += s[previous:loc.start]
			result += "${"
			result += scaffold.BicepName(names[i])
			result += "}"
			previous = loc.stop + 1
		}
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

func printHintsAboutUseHostContainerApp(userResourceName string, usedResourceName string,
	console input.Console, ctx context.Context) {
	if console == nil {
		return
	}
	console.Message(ctx, fmt.Sprintf("Environemnt variables in %s:", userResourceName))
	console.Message(ctx, fmt.Sprintf("%s_BASE_URL=xxx", strings.ToUpper(usedResourceName)))
	console.Message(ctx, fmt.Sprintf("Environemnt variables in %s:", usedResourceName))
	console.Message(ctx, fmt.Sprintf("%s_BASE_URL=xxx", strings.ToUpper(userResourceName)))
}
