// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const (
	SpringHost                 = "azure.springboot"
	databaseTypeEnv            = "SPRING_DATABASE_TYPE"
	databasePasswordEnv        = "SPRING_DATABASE_PASSWORD"
	provisionedDatabaseTypeEnv = "SPRING_DATABASE_PROVISIONED_TYPE"
	azureSubscriptionIDEnv     = "AZURE_SUBSCRIPTION_ID"
	azureResourceGroupEnv      = "AZURE_RESOURCE_GROUP"
	containerAppIDEnv          = "AZURE_CONTAINER_APP_ID"
	postgresServerNameEnv      = "AZURE_POSTGRES_SERVER_NAME"
	postgresServerIDEnv        = "AZURE_POSTGRES_SERVER_ID"
)

type databaseType string

const (
	databaseH2         databaseType = "h2"
	databasePostgreSQL databaseType = "postgresql"
)

type resolvedService struct {
	service *azdext.ServiceConfig
	mode    databaseType
}

func validationError(code, message, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       code,
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: suggestion,
	}
}

func springServices(project *azdext.ProjectConfig) []*azdext.ServiceConfig {
	if project == nil {
		return nil
	}

	names := slices.Sorted(maps.Keys(project.Services))
	services := make([]*azdext.ServiceConfig, 0, len(names))
	for _, name := range names {
		service := project.Services[name]
		if service != nil && service.Host == SpringHost {
			services = append(services, service)
		}
	}
	return services
}

func resolveDatabaseType(
	service *azdext.ServiceConfig,
	values map[string]string,
	lookupProcess func(string) (string, bool),
) (databaseType, error) {
	raw, err := databaseTypeConfig(service)
	if err != nil {
		return "", err
	}

	lookup := func(key string) string {
		if value, ok := values[key]; ok {
			return value
		}
		value, _ := lookupProcess(key)
		return value
	}
	if _, stored := values[databaseTypeEnv]; stored && values[databaseTypeEnv] == "" {
		return "", validationError(
			"empty_database_type",
			fmt.Sprintf("%s is explicitly empty", databaseTypeEnv),
			"set SPRING_DATABASE_TYPE to h2 or postgresql",
		)
	}
	if _, stored := values[databaseTypeEnv]; !stored {
		if processValue, present := lookupProcess(databaseTypeEnv); present && processValue == "" {
			return "", validationError(
				"empty_database_type",
				fmt.Sprintf("%s is explicitly empty", databaseTypeEnv),
				"set SPRING_DATABASE_TYPE to h2 or postgresql",
			)
		}
	}

	expanded, err := osutil.NewExpandableString(raw).Envsubst(lookup)
	if err != nil {
		return "", validationError(
			"invalid_database_type_expression",
			fmt.Sprintf("service %q has an invalid config.database.type expression: %v", service.GetName(), err),
			"use h2, postgresql, or a valid azd environment expression",
		)
	}
	mode := databaseType(strings.ToLower(strings.TrimSpace(expanded)))
	switch mode {
	case databaseH2, databasePostgreSQL:
	default:
		return "", validationError(
			"unsupported_database_type",
			fmt.Sprintf("service %q config.database.type resolved to unsupported value %q", service.GetName(), expanded),
			"set config.database.type or SPRING_DATABASE_TYPE to h2 or postgresql",
		)
	}

	selected, selectedPresent := values[databaseTypeEnv]
	if !selectedPresent {
		selected, selectedPresent = lookupProcess(databaseTypeEnv)
	}
	if selectedPresent && !strings.EqualFold(selected, string(mode)) {
		return "", validationError(
			"conflicting_database_type",
			fmt.Sprintf(
				"service %q config.database.type resolves to %q but %s is %q",
				service.GetName(),
				mode,
				databaseTypeEnv,
				selected,
			),
			"use the same database type in azure.yaml and the selected azd environment",
		)
	}
	return mode, nil
}

func databaseTypeConfig(service *azdext.ServiceConfig) (string, error) {
	if service == nil {
		return "", validationError("missing_service_config", "service configuration is missing", "check azure.yaml")
	}
	if service.Config == nil || len(service.Config.Fields) == 0 {
		return string(databaseH2), nil
	}

	config := service.Config.AsMap()
	for key := range config {
		if key != "database" {
			return "", validationError(
				"unsupported_spring_config",
				fmt.Sprintf("service %q config contains unsupported field %q", service.GetName(), key),
				"remove unsupported fields; azure.springboot currently supports only config.database.type",
			)
		}
	}
	databaseValue, present := config["database"]
	if !present {
		return string(databaseH2), nil
	}
	database, ok := databaseValue.(map[string]any)
	if !ok {
		return "", validationError(
			"invalid_database_config",
			fmt.Sprintf("service %q config.database must be an object", service.GetName()),
			"set config.database.type to h2 or postgresql",
		)
	}
	for key := range database {
		if key != "type" {
			return "", validationError(
				"unsupported_database_config",
				fmt.Sprintf("service %q config.database contains unsupported field %q", service.GetName(), key),
				"remove unsupported fields; azure.springboot currently supports only config.database.type",
			)
		}
	}
	typeValue, present := database["type"]
	if !present {
		return string(databaseH2), nil
	}
	raw, ok := typeValue.(string)
	if !ok {
		return "", validationError(
			"invalid_database_type",
			fmt.Sprintf("service %q config.database.type must be a string", service.GetName()),
			"set config.database.type to h2 or postgresql",
		)
	}
	if raw == "" {
		return "", validationError(
			"empty_database_type",
			fmt.Sprintf("service %q config.database.type is explicitly empty", service.GetName()),
			"set config.database.type to h2 or postgresql",
		)
	}
	return raw, nil
}

func environmentLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		if value, ok := values[key]; ok {
			return value, true
		}
		return os.LookupEnv(key)
	}
}
