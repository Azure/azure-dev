package repository

import (
	"context"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/internal"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// A regex that matches against "likely" well-formed database names
var wellFormedDbNameRegex = regexp.MustCompile(`^[a-zA-Z\-_0-9]*$`)

// infraSpecFromDetect creates an InfraSpec from the results of app detection confirmation,
// prompting for additional inputs if necessary.
func (i *Initializer) infraSpecFromDetect(
	ctx context.Context,
	detect detectConfirm) (scaffold.InfraSpec, error) {
	spec := scaffold.InfraSpec{}
	for database := range detect.Databases {
		if database == appdetect.DbRedis {
			spec.DbRedis = &scaffold.DatabaseRedis{}
			// no further configuration needed for redis
			continue
		}

	dbPrompt:
		for {
			dbName, err := promptDbName(i.console, ctx, database)
			if err != nil {
				return scaffold.InfraSpec{}, err
			}

			switch database {
			case appdetect.DbMongo:
				spec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{
					DatabaseName: dbName,
				}
				break dbPrompt
			case appdetect.DbPostgres:
				if dbName == "" {
					i.console.Message(ctx, "Database name is required.")
					continue
				}
				authType, err := i.getAuthType(ctx)
				if err != nil {
					return scaffold.InfraSpec{}, err
				}
				spec.DbPostgres = &scaffold.DatabasePostgres{
					DatabaseName: dbName,
					AuthType:     authType,
				}
				break dbPrompt
			case appdetect.DbMySql:
				if dbName == "" {
					i.console.Message(ctx, "Database name is required.")
					continue
				}
				authType, err := i.getAuthType(ctx)
				if err != nil {
					return scaffold.InfraSpec{}, err
				}
				spec.DbMySql = &scaffold.DatabaseMySql{
					DatabaseName: dbName,
					AuthType:     authType,
				}
				break dbPrompt
			case appdetect.DbCosmos:
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
				break dbPrompt
			}
			break dbPrompt
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

		port, err := promptPort(i.console, ctx, name, svc)
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
			case appdetect.DbMongo:
				serviceSpec.DbCosmosMongo = &scaffold.DatabaseReference{
					DatabaseName: spec.DbCosmosMongo.DatabaseName,
				}
			case appdetect.DbPostgres:
				serviceSpec.DbPostgres = &scaffold.DatabaseReference{
					DatabaseName: spec.DbPostgres.DatabaseName,
					AuthType:     spec.DbPostgres.AuthType,
				}
			case appdetect.DbMySql:
				serviceSpec.DbMySql = &scaffold.DatabaseReference{
					DatabaseName: spec.DbMySql.DatabaseName,
					AuthType:     spec.DbMySql.AuthType,
				}
			case appdetect.DbCosmos:
				serviceSpec.DbCosmos = spec.DbCosmos
			case appdetect.DbRedis:
				serviceSpec.DbRedis = &scaffold.DatabaseReference{
					DatabaseName: "redis",
				}
			}
		}

		for _, azureDep := range svc.AzureDeps {
			switch azureDep.(type) {
			case appdetect.AzureDepServiceBus:
				serviceSpec.AzureServiceBus = spec.AzureServiceBus
			case appdetect.AzureDepEventHubs:
				serviceSpec.AzureEventHubs = spec.AzureEventHubs
			case appdetect.AzureDepStorageAccount:
				serviceSpec.AzureStorageAccount = spec.AzureStorageAccount
			}
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

func promptDbName(console input.Console, ctx context.Context, database appdetect.DatabaseDep) (string, error) {
	for {
		dbName, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Input the name of the app database (%s)", database.Display()),
			Help: "Hint: App database name\n\n" +
				"Name of the database that the app connects to. " +
				"This database will be created after running azd provision or azd up." +
				"\nYou may be able to skip this step by hitting enter, in which case the database will not be created.",
		})
		if err != nil {
			return "", err
		}

		if strings.ContainsAny(dbName, " ") {
			console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: "Database name contains whitespace. This might not be allowed by the database server.",
			})
			confirm, err := console.Confirm(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Continue with name '%s'?", dbName),
			})
			if err != nil {
				return "", err
			}

			if !confirm {
				continue
			}
		} else if !wellFormedDbNameRegex.MatchString(dbName) {
			console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: "Database name contains special characters. " +
					"This might not be allowed by the database server.",
			})
			confirm, err := console.Confirm(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Continue with name '%s'?", dbName),
			})
			if err != nil {
				return "", err
			}

			if !confirm {
				continue
			}
		}

		return dbName, nil
	}
}

func promptPort(
	console input.Console,
	ctx context.Context,
	name string,
	svc appdetect.Project) (int, error) {
	if svc.Docker == nil || svc.Docker.Path == "" { // using default builder from azd
		if svc.Language == appdetect.Java {
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
	switch dependency := azureDep.(type) {
	case appdetect.AzureDepServiceBus:
		authType, err := i.chooseAuthTypeByPrompt(ctx, azureDep.ResourceDisplay())
		if err != nil {
			return err
		}
		spec.AzureServiceBus = &scaffold.AzureDepServiceBus{
			IsJms:    dependency.IsJms,
			Queues:   dependency.Queues,
			AuthType: authType,
		}
	case appdetect.AzureDepEventHubs:
		authType, err := i.chooseAuthTypeByPrompt(ctx, azureDep.ResourceDisplay())
		if err != nil {
			return err
		}
		spec.AzureEventHubs = &scaffold.AzureDepEventHubs{
			EventHubNames: dependency.Names,
			AuthType:      authType,
			UseKafka:      dependency.UseKafka,
		}
	case appdetect.AzureDepStorageAccount:
		authType, err := i.chooseAuthTypeByPrompt(ctx, azureDep.ResourceDisplay())
		if err != nil {
			return err
		}
		spec.AzureStorageAccount = &scaffold.AzureDepStorageAccount{
			ContainerNames: dependency.ContainerNames,
			AuthType:       authType,
		}
	}
	return nil
}

func (i *Initializer) chooseAuthTypeByPrompt(ctx context.Context, serviceName string) (internal.AuthType, error) {
	portOptions := []string{
		"User assigned managed identity",
		"Connection string",
	}
	selection, err := i.console.Select(ctx, input.ConsoleOptions{
		Message: "Choose auth type for '" + serviceName + "'?",
		Options: portOptions,
	})
	if err != nil {
		return internal.AuthTypeUnspecified, err
	}
	if selection == 0 {
		return internal.AuthtypeManagedIdentity, nil
	} else {
		return internal.AuthtypeConnectionString, nil
	}
}

func (i *Initializer) getAuthType(ctx context.Context) (internal.AuthType, error) {
	authType := internal.AuthTypeUnspecified
	selection, err := i.console.Select(ctx, input.ConsoleOptions{
		Message: "Input the authentication type you want:",
		Options: []string{
			"Use user assigned managed identity",
			"Use username and password",
		},
	})
	if err != nil {
		return authType, err
	}
	switch selection {
	case 0:
		authType = internal.AuthtypeManagedIdentity
	case 1:
		authType = internal.AuthtypePassword
	default:
		panic("unhandled selection")
	}
	return authType, nil
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
