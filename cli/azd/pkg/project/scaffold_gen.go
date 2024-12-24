// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
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
func infraFs(ctx context.Context, prjConfig *ProjectConfig, console input.Console) (fs.FS, error) {
	t, err := scaffold.Load()
	if err != nil {
		return nil, fmt.Errorf("loading scaffold templates: %w", err)
	}

	infraSpec, err := infraSpec(prjConfig, console, ctx)
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
	prjConfig *ProjectConfig, console input.Console) (*Infra, error) {
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
func infraFsForProject(ctx context.Context, prjConfig *ProjectConfig, console input.Console) (fs.FS, error) {
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

func infraSpec(projectConfig *ProjectConfig, console input.Console, ctx context.Context) (*scaffold.InfraSpec, error) {
	infraSpec := scaffold.InfraSpec{}

	for _, res := range projectConfig.Resources {
		switch res.Type {
		case ResourceTypeDbRedis:
			infraSpec.DbRedis = &scaffold.DatabaseRedis{}
		case ResourceTypeDbMongo:
			infraSpec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{
				DatabaseName: res.Name,
			}
		case ResourceTypeDbPostgres:
			infraSpec.DbPostgres = &scaffold.DatabasePostgres{
				DatabaseName: res.Name,
				DatabaseUser: "pgadmin",
				AuthType:     res.Props.(PostgresProps).AuthType,
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
			svcSpec.Envs = append(svcSpec.Envs, serviceConfigEnv(projectConfig.Services[res.Name])...)
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
		err := scaffold.AddNewEnvironmentVariable(svcSpec, envVar.Name, evaluatedValue)
		if err != nil {
			return err
		}
	}

	port := props.Port
	if port < 1 || port > 65535 {
		return fmt.Errorf("port value %d for host %s must be between 1 and 65535", port, res.Name)
	}

	svcSpec.Port = port
	return nil
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
			var err error
			switch usedResource.Type {
			case ResourceTypeDbPostgres:
				err = scaffold.BindToPostgres(userSpec, infraSpec.DbPostgres)
			case ResourceTypeDbMongo:
				err = scaffold.BindToMongoDb(userSpec, infraSpec.DbCosmosMongo)
			case ResourceTypeDbRedis:
				err = scaffold.BindToRedis(userSpec, infraSpec.DbRedis)
			case ResourceTypeOpenAiModel:
				err = scaffold.BindToAIModels(userSpec, usedResource.Name)
			case ResourceTypeHostContainerApp:
				usedSpec := getServiceSpecByName(infraSpec, usedResource.Name)
				if usedSpec == nil {
					return fmt.Errorf("'%s' uses '%s', but %s doesn't exist", userSpec.Name, usedResource.Name,
						usedResource.Name)
				}
				scaffold.BindToContainerApp(userSpec, usedSpec)
			default:
				return fmt.Errorf("resource (%s) uses (%s), but the type of (%s) is (%s), which is unsupported",
					userResource.Name, usedResource.Name, usedResource.Name, usedResource.Type)
			}
			if err != nil {
				return err
			}
		}
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
				"The 'uses' relationship is implemented by environment variables. \n"+
				"Please make sure your application used the right environment variable. \n"+
				"Here is the list of environment variables: ",
				userResourceName, usedResourceName))
			var variables []scaffold.Env
			var err error
			switch usedResource.Type {
			case ResourceTypeDbPostgres:
				variables, err = scaffold.GetServiceBindingEnvsForPostgres(*infraSpec.DbPostgres)
			case ResourceTypeDbMongo:
				variables = scaffold.GetServiceBindingEnvsForMongo()
			case ResourceTypeDbRedis:
				variables = scaffold.GetServiceBindingEnvsForRedis()
			case ResourceTypeHostContainerApp:
				printHintsAboutUseHostContainerApp(userResourceName, usedResourceName, console, ctx)
			default:
				return fmt.Errorf("resource (%s) uses (%s), but the type of (%s) is (%s), "+
					"which is doesn't add necessary environment variable",
					userResource.Name, usedResource.Name, usedResource.Name, usedResource.Type)
			}
			if err != nil {
				return err
			}
			for _, variable := range variables {
				console.Message(ctx, fmt.Sprintf("  %s=xxx", variable.Name))
			}
			console.Message(ctx, "\n")
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

func getServiceSpecByName(infraSpec *scaffold.InfraSpec, name string) *scaffold.ServiceSpec {
	for i := range infraSpec.Services {
		if infraSpec.Services[i].Name == name {
			return &infraSpec.Services[i]
		}
	}
	return nil
}

// todo: merge it into scaffold.BindToContainerApp
func printHintsAboutUseHostContainerApp(userResourceName string, usedResourceName string,
	console input.Console, ctx context.Context) {
	if console == nil {
		return
	}
	console.Message(ctx, fmt.Sprintf("Environment variables in %s:", userResourceName))
	console.Message(ctx, fmt.Sprintf("%s_BASE_URL=xxx", strings.ToUpper(usedResourceName)))
	console.Message(ctx, fmt.Sprintf("Environment variables in %s:", usedResourceName))
	console.Message(ctx, fmt.Sprintf("%s_BASE_URL=xxx", strings.ToUpper(userResourceName)))
}

func serviceConfigEnv(svcConfig *ServiceConfig) []scaffold.Env {
	var envs []scaffold.Env
	if svcConfig != nil {
		for key, val := range svcConfig.Env {
			envs = append(envs, scaffold.Env{
				Name:  key,
				Value: val,
			})
		}
	}
	return envs
}
