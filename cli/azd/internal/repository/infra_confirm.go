// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package repository

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/cmd/add"
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

	hasDb := false
	for database := range detect.Databases {
		hasDb = true
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

				spec.DbPostgres = &scaffold.DatabasePostgres{
					DatabaseName: dbName,
				}
			}
			break dbPrompt
		}
	}

	if hasDb {
		spec.KeyVault = &scaffold.KeyVault{}
	}

	for _, svc := range detect.Services {
		name := names.LabelName(filepath.Base(svc.Path))
		serviceSpec := scaffold.ServiceSpec{
			Name: name,
			Port: -1,
			Host: scaffold.ContainerAppKind,
		}

		port, err := add.PromptPort(i.console, ctx, name, svc)
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
				}
			case appdetect.DbRedis:
				serviceSpec.DbRedis = &scaffold.DatabaseReference{
					DatabaseName: "redis",
				}
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
