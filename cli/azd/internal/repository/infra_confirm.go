package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// infraSpecFromDetect creates an InfraSpec from the results of app detection confirmation,
// prompting for additional inputs if necessary.
func (i *Initializer) infraSpecFromDetect(
	ctx context.Context,
	detect *detectConfirm) (scaffold.InfraSpec, error) {
	spec := scaffold.InfraSpec{}
	for database := range detect.Databases {
		switch database {
		case appdetect.DbRedis:
			spec.DbRedis = &scaffold.DatabaseRedis{}
		case appdetect.DbMongo:
			dbName, err := getDatabaseName(database, detect, i.console, ctx)
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
			spec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{
				DatabaseName: dbName,
			}
		case appdetect.DbPostgres:
			dbName, err := getDatabaseName(database, detect, i.console, ctx)
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
			authType, err := chooseAuthTypeByPrompt(
				database.Display(),
				[]internal.AuthType{internal.AuthTypeUserAssignedManagedIdentity, internal.AuthTypePassword},
				ctx,
				i.console)
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
			continueProvision, err := checkPasswordlessConfigurationAndContinueProvision(database,
				authType, detect, i.console, ctx)
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
			if !continueProvision {
				continue
			}
			spec.DbPostgres = &scaffold.DatabasePostgres{
				DatabaseName: dbName,
				AuthType:     authType,
			}
		case appdetect.DbMySql:
			dbName, err := getDatabaseName(database, detect, i.console, ctx)
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
			authType, err := chooseAuthTypeByPrompt(
				database.Display(),
				[]internal.AuthType{internal.AuthTypeUserAssignedManagedIdentity, internal.AuthTypePassword},
				ctx,
				i.console)
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
			continueProvision, err := checkPasswordlessConfigurationAndContinueProvision(database,
				authType, detect, i.console, ctx)
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
			if !continueProvision {
				continue
			}
			spec.DbMySql = &scaffold.DatabaseMySql{
				DatabaseName: dbName,
				AuthType:     authType,
			}
		case appdetect.DbCosmos:
			dbName, err := getDatabaseName(database, detect, i.console, ctx)
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
			if dbName == "" {
				i.console.Message(ctx, "Database name is required.")
				continue
			}
			containers, err := detectCosmosSqlDatabaseContainersInDirectory(detect.root)
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
			spec.DbCosmos = &scaffold.DatabaseCosmosAccount{
				DatabaseName: dbName,
				Containers:   containers,
			}
		}
	}

	for _, azureDep := range detect.AzureDeps {
		err := i.buildInfraSpecByAzureDep(ctx, azureDep.first, &spec)
		if err != nil {
			return scaffold.InfraSpec{}, err
		}
	}

	for _, svc := range detect.Services {
		name := names.LabelName(filepath.Base(svc.Path))
		serviceSpec := scaffold.ServiceSpec{
			Name: name,
			Port: -1,
		}

		port, err := GetOrPromptPort(i.console, ctx, name, svc)
		if err != nil {
			return scaffold.InfraSpec{}, err
		}
		serviceSpec.Port = port

		for _, framework := range svc.Dependencies {
			if framework.IsWebUIFramework() {
				serviceSpec.Frontend = &scaffold.Frontend{}
			}
		}

		for _, db := range svc.DatabaseDeps {
			// filter out databases that were removed
			if _, ok := detect.Databases[db]; !ok {
				continue
			}
			switch db {
			case appdetect.DbPostgres:
				err = scaffold.BindToPostgres(&serviceSpec, spec.DbPostgres)
			case appdetect.DbMySql:
				err = scaffold.BindToMySql(&serviceSpec, spec.DbMySql)
			case appdetect.DbMongo:
				err = scaffold.BindToMongoDb(&serviceSpec, spec.DbCosmosMongo)
			case appdetect.DbCosmos:
				err = scaffold.BindToCosmosDb(&serviceSpec, spec.DbCosmos)
			case appdetect.DbRedis:
				err = scaffold.BindToRedis(&serviceSpec, spec.DbRedis)
			}
			if err != nil {
				return scaffold.InfraSpec{}, err
			}
		}

		for _, azureDep := range svc.AzureDeps {
			switch azureDep.(type) {
			case appdetect.AzureDepServiceBus:
				err = scaffold.BindToServiceBus(&serviceSpec, spec.AzureServiceBus)
			case appdetect.AzureDepEventHubs:
				err = scaffold.BindToEventHubs(&serviceSpec, spec.AzureEventHubs)
			case appdetect.AzureDepStorageAccount:
				err = scaffold.BindToStorageAccount(&serviceSpec, spec.AzureStorageAccount)
			}
		}
		if err != nil {
			return scaffold.InfraSpec{}, err
		}
		spec.Services = append(spec.Services, serviceSpec)
	}

	backends := []scaffold.ServiceReference{}
	frontends := []scaffold.ServiceReference{}
	for idx := range spec.Services {
		if spec.Services[idx].Frontend == nil && spec.Services[idx].Port != 0 {
			backends = append(backends, scaffold.ServiceReference{
				Name: spec.Services[idx].Name,
			})

			spec.Services[idx].Backend = &scaffold.Backend{}
		} else {
			frontends = append(frontends, scaffold.ServiceReference{
				Name: spec.Services[idx].Name,
			})
		}
	}

	// Link services together
	for _, service := range spec.Services {
		if service.Frontend != nil && len(backends) > 0 {
			service.Frontend.Backends = backends
		}

		if service.Backend != nil && len(frontends) > 0 {
			service.Backend.Frontends = frontends
		}
	}

	return spec, nil
}

func getDatabaseName(database appdetect.DatabaseDep, detect *detectConfirm,
	console input.Console, ctx context.Context) (string, error) {
	dbName := getDatabaseNameFromProjectMetadata(detect, database)
	if dbName != "" {
		return dbName, nil
	}
	for {
		dbName, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Input the databaseName for %s "+
				"(Not databaseServerName. This url can explain the difference: "+
				"'jdbc:mysql://databaseServerName:3306/databaseName'):", database.Display()),
			Help: "Hint: App database name\n\n" +
				"Name of the database that the app connects to. " +
				"This database will be created after running azd provision or azd up.\n" +
				"You may be able to skip this step by hitting enter, in which case the database will not be created.",
		})
		if err != nil {
			return "", err
		}
		if appdetect.IsValidDatabaseName(dbName) {
			return dbName, nil
		} else {
			console.Message(ctx, "Invalid database name. Please choose another name.")
		}
	}
}

func getDatabaseNameFromProjectMetadata(detect *detectConfirm, database appdetect.DatabaseDep) string {
	result := ""
	for _, service := range detect.Services {
		name := service.Metadata.DatabaseNameInPropertySpringDatasourceUrl[database]
		if name != "" {
			if result == "" {
				result = name
			} else {
				// different project configured different db name, not use any of them.
				return ""
			}
		}
	}
	return result
}

func promptPortNumber(console input.Console, ctx context.Context, promptMessage string) (int, error) {
	var port int
	for {
		val, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: promptMessage,
		})
		if err != nil {
			return -1, err
		}

		port, err = strconv.Atoi(val)
		if err != nil {
			console.Message(ctx, "Port must be an integer.")
			continue
		}

		if port < 1 || port > 65535 {
			console.Message(ctx, "Port must be a value between 1 and 65535.")
			continue
		}

		break
	}
	return port, nil
}

// GetOrPromptPort prompts for port selection from an appdetect project.
func GetOrPromptPort(
	console input.Console,
	ctx context.Context,
	name string,
	svc appdetect.Project) (int, error) {
	if svc.Metadata.ServerPort != "" {
		return strconv.Atoi(svc.Metadata.ServerPort)
	}
	if svc.Docker == nil || svc.Docker.Path == "" { // using default builder from azd
		if svc.Language == appdetect.Java || svc.Language == appdetect.DotNet {
			if svc.Metadata.ContainsDependencySpringCloudEurekaServer {
				return 8761, nil
			}
			if svc.Metadata.ContainsDependencySpringCloudConfigServer {
				return 8888, nil
			}
			return 8080, nil
		}
		return 80, nil
	}

	// a custom Dockerfile is provided
	ports := svc.Docker.Ports
	switch len(ports) {
	case 1: // only one port was exposed, that's the one
		return ports[0].Number, nil
	case 0: // no ports exposed, prompt for port
		port, err := promptPortNumber(console, ctx, "What port does '"+name+"' listen on?")
		if err != nil {
			return -1, err
		}
		return port, nil
	}

	// multiple ports exposed, prompt for selection
	var portOptions []string
	for _, port := range ports {
		portOptions = append(portOptions, strconv.Itoa(port.Number))
	}
	portOptions = append(portOptions, "Other")

	selection, err := console.Select(ctx, input.ConsoleOptions{
		Message: "What port does '" + name + "' listen on?",
		Options: portOptions,
	})
	if err != nil {
		return -1, err
	}

	if selection < len(ports) { // user selected a port
		return ports[selection].Number, nil
	}

	// user selected 'Other', prompt for port
	port, err := promptPortNumber(console, ctx, "Provide the port number for '"+name+"':")
	if err != nil {
		return -1, err
	}

	return port, nil
}

func (i *Initializer) buildInfraSpecByAzureDep(
	ctx context.Context,
	azureDep appdetect.AzureDep,
	spec *scaffold.InfraSpec) error {
	authType, err := chooseAuthTypeByPrompt(
		azureDep.ResourceDisplay(),
		[]internal.AuthType{internal.AuthTypeUserAssignedManagedIdentity, internal.AuthTypeConnectionString},
		ctx,
		i.console)
	if err != nil {
		return err
	}
	switch dependency := azureDep.(type) {
	case appdetect.AzureDepServiceBus:
		spec.AzureServiceBus = &scaffold.AzureDepServiceBus{
			IsJms:    dependency.IsJms,
			Queues:   dependency.Queues,
			AuthType: authType,
		}
	case appdetect.AzureDepEventHubs:
		spec.AzureEventHubs = &scaffold.AzureDepEventHubs{
			EventHubNames:     appdetect.DistinctValues(dependency.EventHubsNamePropertyMap),
			AuthType:          authType,
			UseKafka:          dependency.UseKafka(),
			SpringBootVersion: dependency.SpringBootVersion,
		}
	case appdetect.AzureDepStorageAccount:
		spec.AzureStorageAccount = &scaffold.AzureDepStorageAccount{
			ContainerNames: appdetect.DistinctValues(dependency.ContainerNamePropertyMap),
			AuthType:       authType,
		}
	}
	return nil
}

func detectCosmosSqlDatabaseContainersInDirectory(root string) ([]scaffold.CosmosSqlDatabaseContainer, error) {
	var result []scaffold.CosmosSqlDatabaseContainer
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".java" {
			container, err := detectCosmosSqlDatabaseContainerInFile(path)
			if err != nil {
				return err
			}
			if len(container.ContainerName) != 0 {
				result = append(result, container)
			}
		}
		return nil
	})
	return result, err
}

func detectCosmosSqlDatabaseContainerInFile(filePath string) (scaffold.CosmosSqlDatabaseContainer, error) {
	var result scaffold.CosmosSqlDatabaseContainer
	result.PartitionKeyPaths = make([]string, 0)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return result, err
	}
	// todo:
	// 1. Maybe "@Container" is not "com.azure.spring.data.cosmos.core.mapping.Container"
	// 2. Maybe "@Container" is imported by "com.azure.spring.data.cosmos.core.mapping.*"
	containerRegex := regexp.MustCompile(`@Container\s*\(containerName\s*=\s*"([^"]+)"\)`)
	partitionKeyRegex := regexp.MustCompile(`@PartitionKey\s*(?:\n\s*)?(?:private|public|protected)?\s*\w+\s+(\w+);`)

	matches := containerRegex.FindAllStringSubmatch(string(content), -1)
	if len(matches) != 1 {
		return result, nil
	}
	result.ContainerName = matches[0][1]

	matches = partitionKeyRegex.FindAllStringSubmatch(string(content), -1)
	for _, match := range matches {
		result.PartitionKeyPaths = append(result.PartitionKeyPaths, match[1])
	}
	return result, nil
}
