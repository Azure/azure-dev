// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
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
func infraFs(_ context.Context, prjConfig *ProjectConfig) (fs.FS, error) {
	t, err := scaffold.Load()
	if err != nil {
		return nil, fmt.Errorf("loading scaffold templates: %w", err)
	}

	infraSpec, err := infraSpec(prjConfig)
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
	prjConfig *ProjectConfig) (*Infra, error) {
	tmpDir, err := os.MkdirTemp("", "azd-infra")
	if err != nil {
		return nil, fmt.Errorf("creating temporary directory: %w", err)
	}

	files, err := infraFs(ctx, prjConfig)
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
func infraFsForProject(ctx context.Context, prjConfig *ProjectConfig) (fs.FS, error) {
	infraFS, err := infraFs(ctx, prjConfig)
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

func infraSpec(projectConfig *ProjectConfig) (*scaffold.InfraSpec, error) {
	infraSpec := scaffold.InfraSpec{}
	// backends -> frontends
	backendMapping := map[string]string{}

	for _, res := range projectConfig.Resources {
		switch res.Type {
		case ResourceTypeDbRedis:
			infraSpec.DbRedis = &scaffold.DatabaseRedis{}
		case ResourceTypeDbMongo:
			infraSpec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{
				DatabaseName: res.Props.(MongoDBProps).DatabaseName,
			}
		case ResourceTypeDbPostgres:
			infraSpec.DbPostgres = &scaffold.DatabasePostgres{
				DatabaseName: res.Props.(PostgresProps).DatabaseName,
				DatabaseUser: "pgadmin",
				AuthType:     res.Props.(PostgresProps).AuthType,
			}
		case ResourceTypeDbMySQL:
			infraSpec.DbMySql = &scaffold.DatabaseMySql{
				DatabaseName: res.Props.(MySQLProps).DatabaseName,
				DatabaseUser: "mysqladmin",
				AuthType:     res.Props.(MySQLProps).AuthType,
			}
		case ResourceTypeDbCosmos:
			infraSpec.DbCosmos = &scaffold.DatabaseCosmosAccount{
				DatabaseName: res.Props.(CosmosDBProps).DatabaseName,
			}
			containers := res.Props.(CosmosDBProps).Containers
			for _, container := range containers {
				infraSpec.DbCosmos.Containers = append(infraSpec.DbCosmos.Containers, scaffold.CosmosSqlDatabaseContainer{
					ContainerName:     container.ContainerName,
					PartitionKeyPaths: container.PartitionKeyPaths,
				})
			}
		case ResourceTypeMessagingServiceBus:
			props := res.Props.(ServiceBusProps)
			infraSpec.AzureServiceBus = &scaffold.AzureDepServiceBus{
				Queues:   props.Queues,
				AuthType: props.AuthType,
				IsJms:    props.IsJms,
			}
		case ResourceTypeMessagingEventHubs:
			props := res.Props.(EventHubsProps)
			infraSpec.AzureEventHubs = &scaffold.AzureDepEventHubs{
				EventHubNames: props.EventHubNames,
				AuthType:      props.AuthType,
				UseKafka:      false,
			}
		case ResourceTypeMessagingKafka:
			props := res.Props.(KafkaProps)
			infraSpec.AzureEventHubs = &scaffold.AzureDepEventHubs{
				EventHubNames: props.Topics,
				AuthType:      props.AuthType,
				UseKafka:      true,
			}
		case ResourceTypeHostContainerApp:
			svcSpec := scaffold.ServiceSpec{
				Name: res.Name,
				Port: -1,
			}

			err := mapContainerApp(res, &svcSpec, &infraSpec)
			if err != nil {
				return nil, err
			}

			err = mapHostUses(res, &svcSpec, backendMapping, projectConfig)
			if err != nil {
				return nil, err
			}

			infraSpec.Services = append(infraSpec.Services, svcSpec)
		case ResourceTypeOpenAiModel:
			props := res.Props.(AIModelProps)
			if len(props.Model.Name) == 0 {
				return nil, fmt.Errorf("resources.%s.model is required", res.Name)
			}

			if len(props.Model.Version) == 0 {
				return nil, fmt.Errorf("resources.%s.version is required", res.Name)
			}

			infraSpec.AIModels = append(infraSpec.AIModels, scaffold.AIModel{
				Name: res.Name,
				Model: scaffold.AIModelModel{
					Name:    props.Model.Name,
					Version: props.Model.Version,
				},
			})
		}
	}

	// create reverse frontends -> backends mapping
	for i := range infraSpec.Services {
		svc := &infraSpec.Services[i]
		if front, ok := backendMapping[svc.Name]; ok {
			if svc.Backend == nil {
				svc.Backend = &scaffold.Backend{}
			}
			svc.Backend.Frontends = append(svc.Backend.Frontends, scaffold.ServiceReference{Name: front})
		}
		if infraSpec.DbPostgres != nil {
			svc.DbPostgres = &scaffold.DatabaseReference{
				DatabaseName: infraSpec.DbPostgres.DatabaseName,
				AuthType:     infraSpec.DbPostgres.AuthType,
			}
		}
		if infraSpec.DbMySql != nil {
			svc.DbMySql = &scaffold.DatabaseReference{
				DatabaseName: infraSpec.DbMySql.DatabaseName,
				AuthType:     infraSpec.DbMySql.AuthType,
			}
		}
		if infraSpec.DbRedis != nil {
			svc.DbRedis = &scaffold.DatabaseReference{
				DatabaseName: "redis",
			}
		}
		if infraSpec.DbCosmosMongo != nil {
			svc.DbCosmosMongo = &scaffold.DatabaseReference{
				DatabaseName: infraSpec.DbCosmosMongo.DatabaseName,
			}
		}
		if infraSpec.DbCosmos != nil {
			svc.DbCosmos = &scaffold.DatabaseCosmosAccount{
				DatabaseName: infraSpec.DbCosmos.DatabaseName,
				Containers:   infraSpec.DbCosmos.Containers,
			}
		}
	}

	slices.SortFunc(infraSpec.Services, func(a, b scaffold.ServiceSpec) int {
		return strings.Compare(a.Name, b.Name)
	})

	return &infraSpec, nil
}

func mapContainerApp(res *ResourceConfig, svcSpec *scaffold.ServiceSpec, infraSpec *scaffold.InfraSpec) error {
	props := res.Props.(ContainerAppProps)
	for _, envVar := range props.Env {
		if len(envVar.Value) == 0 && len(envVar.Secret) == 0 {
			return fmt.Errorf(
				"environment variable %s for host %s is invalid: both value and secret are empty",
				envVar.Name,
				res.Name)
		}

		if len(envVar.Value) > 0 && len(envVar.Secret) > 0 {
			return fmt.Errorf(
				"environment variable %s for host %s is invalid: both value and secret are set",
				envVar.Name,
				res.Name)
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
		svcSpec.Env[envVar.Name] = evaluatedValue
	}

	port := props.Port
	if port < 1 || port > 65535 {
		return fmt.Errorf("port value %d for host %s must be between 1 and 65535", port, res.Name)
	}

	svcSpec.Port = port
	return nil
}

func mapHostUses(
	res *ResourceConfig,
	svcSpec *scaffold.ServiceSpec,
	backendMapping map[string]string,
	prj *ProjectConfig) error {
	for _, use := range res.Uses {
		useRes, ok := prj.Resources[use]
		if !ok {
			return fmt.Errorf("resource %s uses %s, which does not exist", res.Name, use)
		}

		switch useRes.Type {
		case ResourceTypeDbMongo:
			svcSpec.DbCosmosMongo = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeDbPostgres:
			svcSpec.DbPostgres = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeDbRedis:
			svcSpec.DbRedis = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeHostContainerApp:
			if svcSpec.Frontend == nil {
				svcSpec.Frontend = &scaffold.Frontend{}
			}

			svcSpec.Frontend.Backends = append(svcSpec.Frontend.Backends,
				scaffold.ServiceReference{Name: use})
			backendMapping[use] = res.Name // record the backend -> frontend mapping
		case ResourceTypeOpenAiModel:
			svcSpec.AIModels = append(svcSpec.AIModels, scaffold.AIModelReference{Name: use})
		}
	}

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
