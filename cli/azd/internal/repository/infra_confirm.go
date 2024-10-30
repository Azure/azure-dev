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
			dbName, err := i.console.Prompt(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Input the name of the app database (%s)", database.Display()),
				Help: "Hint: App database name\n\n" +
					"Name of the database that the app connects to. " +
					"This database will be created after running azd provision or azd up." +
					"\nYou may be able to skip this step by hitting enter, in which case the database will not be created.",
			})
			if err != nil {
				return scaffold.InfraSpec{}, err
			}

			if strings.ContainsAny(dbName, " ") {
				i.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: "Database name contains whitespace. This might not be allowed by the database server.",
				})
				confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
					Message: fmt.Sprintf("Continue with name '%s'?", dbName),
				})
				if err != nil {
					return scaffold.InfraSpec{}, err
				}

				if !confirm {
					continue dbPrompt
				}
			} else if !wellFormedDbNameRegex.MatchString(dbName) {
				i.console.MessageUxItem(ctx, &ux.WarningMessage{
					Description: "Database name contains special characters. " +
						"This might not be allowed by the database server.",
				})
				confirm, err := i.console.Confirm(ctx, input.ConsoleOptions{
					Message: fmt.Sprintf("Continue with name '%s'?", dbName),
				})
				if err != nil {
					return scaffold.InfraSpec{}, err
				}

				if !confirm {
					continue dbPrompt
				}
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

		if svc.Docker == nil || svc.Docker.Path == "" {
			// default builder always specifies port 80
			serviceSpec.Port = 80
			if svc.Language == appdetect.Java {
				serviceSpec.Port = 8080
			}
		} else {
			ports := svc.Docker.Ports
			if len(ports) == 0 {
				port, err := i.getPortByPrompt(ctx, "What port does '"+serviceSpec.Name+"' listen on?")
				if err != nil {
					return scaffold.InfraSpec{}, err
				}
				serviceSpec.Port = port
			} else if len(ports) == 1 {
				serviceSpec.Port = ports[0].Number
			} else {
				var portOptions []string
				for _, port := range ports {
					portOptions = append(portOptions, strconv.Itoa(port.Number))
				}
				inputAnotherPortOption := "Other"
				portOptions = append(portOptions, inputAnotherPortOption)
				selection, err := i.console.Select(ctx, input.ConsoleOptions{
					Message: "What port does '" + serviceSpec.Name + "' listen on?",
					Options: portOptions,
				})
				if err != nil {
					return scaffold.InfraSpec{}, err
				}
				if selection < len(ports) {
					serviceSpec.Port = ports[selection].Number
				} else {
					port, err := i.getPortByPrompt(ctx, "Provide the port number for '"+serviceSpec.Name+"':")
					if err != nil {
						return scaffold.InfraSpec{}, err
					}
					serviceSpec.Port = port
				}
			}
		}

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

func (i *Initializer) getPortByPrompt(ctx context.Context, promptMessage string) (int, error) {
	var port int
	for {
		val, err := i.console.Prompt(ctx, input.ConsoleOptions{
			Message: promptMessage,
		})
		if err != nil {
			return -1, err
		}

		port, err = strconv.Atoi(val)
		if err != nil {
			i.console.Message(ctx, "Port must be an integer.")
			continue
		}

		if port < 1 || port > 65535 {
			i.console.Message(ctx, "Port must be a value between 1 and 65535.")
			continue
		}

		break
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

		authType := scaffold.AuthType(0)
		switch azureDep.(type) {
		case appdetect.AzureDepServiceBus:
			_authType, err := i.console.Prompt(ctx, input.ConsoleOptions{
				Message: fmt.Sprintf("Input the authentication type you want for (%s), "+
					"1 for connection string, 2 for managed identity", azureDep.ResourceDisplay()),
				Help: "Authentication type:\n\n" +
					"Enter 1 if you want to use connection string to connect to the Service Bus.\n" +
					"Enter 2 if you want to use user assigned managed identity to connect to the Service Bus.",
			})
			if err != nil {
				return err
			}

			if _authType != "1" && _authType != "2" {
				i.console.Message(ctx, "Invalid authentication type. Please enter 0 or 1.")
				continue azureDepPrompt
			}
			if _authType == "1" {
				authType = scaffold.AuthType_PASSWORD
			} else {
				authType = scaffold.AuthType_TOKEN_CREDENTIAL
			}
		}

		switch dependency := azureDep.(type) {
		case appdetect.AzureDepServiceBus:
			spec.AzureServiceBus = &scaffold.AzureDepServiceBus{
				Name:                      azureDepName,
				Queues:                    dependency.Queues,
				AuthUsingConnectionString: authType == scaffold.AuthType_PASSWORD,
				AuthUsingManagedIdentity:  authType == scaffold.AuthType_TOKEN_CREDENTIAL,
			}
			break azureDepPrompt
		}
		break azureDepPrompt
	}
	return nil
}
