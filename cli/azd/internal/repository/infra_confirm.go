package repository

import (
	"context"
	"fmt"
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
					DatabaseName:              dbName,
					AuthUsingManagedIdentity:  authType == scaffold.AuthType_TOKEN_CREDENTIAL,
					AuthUsingUsernamePassword: authType == scaffold.AuthType_PASSWORD,
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
					DatabaseName:              dbName,
					AuthUsingManagedIdentity:  authType == scaffold.AuthType_TOKEN_CREDENTIAL,
					AuthUsingUsernamePassword: authType == scaffold.AuthType_PASSWORD,
				}
				break dbPrompt
			}
			break dbPrompt
		}
	}

	for _, azureDep := range detect.AzureDeps {
		err := i.promptForAzureResource(ctx, azureDep.first, &spec)
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
					DatabaseName:              spec.DbPostgres.DatabaseName,
					AuthUsingManagedIdentity:  spec.DbPostgres.AuthUsingManagedIdentity,
					AuthUsingUsernamePassword: spec.DbPostgres.AuthUsingUsernamePassword,
				}
			case appdetect.DbMySql:
				serviceSpec.DbMySql = &scaffold.DatabaseReference{
					DatabaseName:              spec.DbMySql.DatabaseName,
					AuthUsingManagedIdentity:  spec.DbMySql.AuthUsingManagedIdentity,
					AuthUsingUsernamePassword: spec.DbMySql.AuthUsingUsernamePassword,
				}
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

func (i *Initializer) getAuthType(ctx context.Context) (scaffold.AuthType, error) {
	authType := scaffold.AuthType(0)
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
		authType = scaffold.AuthType_TOKEN_CREDENTIAL
	case 1:
		authType = scaffold.AuthType_PASSWORD
	default:
		panic("unhandled selection")
	}
	return authType, nil
}

func (i *Initializer) promptForAzureResource(
	ctx context.Context,
	azureDep appdetect.AzureDep,
	spec *scaffold.InfraSpec) error {
azureDepPrompt:
	for {
		azureDepName, err := i.console.Prompt(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Input the name of the Azure dependency (%s)", azureDep.ResourceDisplay()),
			Help: "Azure dependency name\n\n" +
				"Name of the Azure dependency that the app connects to. " +
				"This dependency will be created after running azd provision or azd up." +
				"\nYou may be able to skip this step by hitting enter, in which case the dependency will not be created.",
		})
		if err != nil {
			return err
		}

		if strings.ContainsAny(azureDepName, " ") {
			i.console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: "Dependency name contains whitespace. This might not be allowed by the Azure service.",
			})
			confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Continue with name '%s'?", azureDepName),
			})
			if err != nil {
				return err
			}

			if !confirm {
				continue azureDepPrompt
			}
		} else if !wellFormedDbNameRegex.MatchString(azureDepName) {
			i.console.MessageUxItem(ctx, &ux.WarningMessage{
				Description: "Dependency name contains special characters. " +
					"This might not be allowed by the Azure service.",
			})
			confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Continue with name '%s'?", azureDepName),
			})
			if err != nil {
				return err
			}

			if !confirm {
				continue azureDepPrompt
			}
		}

		switch dependency := azureDep.(type) {
		case appdetect.AzureDepServiceBus:
			authType, err := i.chooseAuthType(ctx, azureDepName)
			if err != nil {
				return err
			}
			spec.AzureServiceBus = &scaffold.AzureDepServiceBus{
				Name:                      azureDepName,
				Queues:                    dependency.Queues,
				AuthUsingConnectionString: authType == scaffold.AuthType_PASSWORD,
				AuthUsingManagedIdentity:  authType == scaffold.AuthType_TOKEN_CREDENTIAL,
			}
		case appdetect.AzureDepEventHubs:
			authType, err := i.chooseAuthType(ctx, azureDepName)
			if err != nil {
				return err
			}
			spec.AzureEventHubs = &scaffold.AzureDepEventHubs{
				Name:                      azureDepName,
				EventHubNames:             dependency.Names,
				AuthUsingConnectionString: authType == scaffold.AuthType_PASSWORD,
				AuthUsingManagedIdentity:  authType == scaffold.AuthType_TOKEN_CREDENTIAL,
			}
		case appdetect.AzureDepStorageAccount:
			authType, err := i.chooseAuthType(ctx, azureDepName)
			if err != nil {
				return err
			}
			spec.AzureStorageAccount = &scaffold.AzureDepStorageAccount{
				Name:                      azureDepName,
				ContainerNames:            dependency.ContainerNames,
				AuthUsingConnectionString: authType == scaffold.AuthType_PASSWORD,
				AuthUsingManagedIdentity:  authType == scaffold.AuthType_TOKEN_CREDENTIAL,
			}
		}
		break azureDepPrompt
	}
	return nil
}

func (i *Initializer) chooseAuthType(ctx context.Context, serviceName string) (scaffold.AuthType, error) {
	portOptions := []string{
		"User assigned managed identity",
		"Connection string",
	}
	selection, err := i.console.Select(ctx, input.ConsoleOptions{
		Message: "Choose auth type for '" + serviceName + "'?",
		Options: portOptions,
	})
	if err != nil {
		return scaffold.AUTH_TYPE_UNSPECIFIED, err
	}
	if selection == 0 {
		return scaffold.AuthType_TOKEN_CREDENTIAL, nil
	} else {
		return scaffold.AuthType_PASSWORD, nil
	}
}
