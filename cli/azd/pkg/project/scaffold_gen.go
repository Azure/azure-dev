// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/azure/azure-dev/internal/scaffold"
	"github.com/azure/azure-dev/pkg/environment"
	"github.com/azure/azure-dev/pkg/infra"
	"github.com/azure/azure-dev/pkg/infra/provisioning"
	"github.com/azure/azure-dev/pkg/osutil"
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

		return os.WriteFile(target, contents, osutil.PermissionFile)
	})
	if err != nil {
		return nil, fmt.Errorf("writing infrastructure: %w", err)
	}

	return &Infra{
		Options: provisioning.Options{
			Provider: provisioning.Bicep,
			Path:     tmpDir,
			Module:   DefaultProvisioningOptions.Module,
		},
		cleanupDir: tmpDir,
		IsCompose:  true,
	}, nil
}

// Generates the filesystem of all infrastructure files to be placed, rooted at the project directory.
// The content only includes `./infra` currently.
func infraFsForProject(ctx context.Context, prjConfig *ProjectConfig) (fs.FS, error) {
	infraFS, err := infraFs(ctx, prjConfig)
	if err != nil {
		return nil, err
	}

	infraPathPrefix := DefaultProvisioningOptions.Path
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
	existingMap := map[string]*scaffold.ExistingResource{}
	// backends -> frontends
	backendMapping := map[string]string{}

	// Create a "virtual" copy since we're adding any implicitly dependent resources
	// that are unrepresented by the current user-provided schema
	resources := maps.Clone(projectConfig.Resources)
	keys := slices.Sorted(maps.Keys(resources))

	// First pass
	for _, k := range keys {
		res := resources[k]
		// Add any implicit dependencies
		dependencies := DependentResourcesOf(res)
		for _, dep := range dependencies {
			if _, exists := resources[dep.Name]; !exists {
				resources[dep.Name] = dep
			}
		}

		if res.Existing { // handle existing flow
			resourceMeta, ok := scaffold.ResourceMetaFromType(res.Type.AzureResourceType())
			if !ok {
				return nil, fmt.Errorf("resource type '%s' is not currently supported for existing", string(res.Type))
			}

			existing := scaffold.ExistingResource{
				Name:             "existing" + scaffold.BicepNameInfix(res.Name),
				ApiVersion:       resourceMeta.ApiVersion,
				ResourceIdEnvVar: infra.ResourceIdName(res.Name),
				ResourceType:     resourceMeta.ResourceType,
				RoleAssignments:  resourceMeta.RoleAssignments.Write,
			}

			if resourceMeta.ParentForEval != "" {
				existing.ResourceType = resourceMeta.ParentForEval
			}

			if res.Type == ResourceTypeKeyVault {
				// For Key Vault, we grant read access to secrets by default
				existing.RoleAssignments = resourceMeta.RoleAssignments.Read
			}

			infraSpec.Existing = append(infraSpec.Existing, existing)
			existingMap[res.Name] = &existing
			continue
		}
	}

	// Re-calculate keys to include any added dependent resources
	keys = slices.Sorted(maps.Keys(resources))

	for _, k := range keys {
		res := resources[k]
		if res.Existing {
			continue
		}

		switch res.Type {
		case ResourceTypeDbRedis:
			infraSpec.DbRedis = &scaffold.DatabaseRedis{}
		case ResourceTypeDbMongo:
			infraSpec.DbCosmosMongo = &scaffold.DatabaseCosmosMongo{
				DatabaseName: res.Name,
			}
		case ResourceTypeDbCosmos:
			props := res.Props.(CosmosDBProps)
			containers := make([]scaffold.CosmosSqlDatabaseContainer, 0)
			for _, c := range props.Containers {
				containers = append(containers, scaffold.CosmosSqlDatabaseContainer{
					ContainerName:     c.Name,
					PartitionKeyPaths: c.PartitionKeys,
				})
			}
			infraSpec.DbCosmos = &scaffold.DatabaseCosmos{
				DatabaseName: res.Name,
				Containers:   containers,
			}
		case ResourceTypeDbPostgres:
			infraSpec.DbPostgres = &scaffold.DatabasePostgres{
				DatabaseName: res.Name,
			}
		case ResourceTypeDbMySql:
			infraSpec.DbMySql = &scaffold.DatabaseMysql{
				DatabaseName: res.Name,
			}
		case ResourceTypeHostAppService:
			svcConfig, ok := projectConfig.Services[res.Name]
			if !ok {
				return nil, fmt.Errorf("service %s not found in project config", res.Name)
			}
			svcSpec := scaffold.ServiceSpec{
				Name: res.Name,
				Port: -1,
				Env:  map[string]string{},
				Host: scaffold.AppServiceKind,
			}

			err := mapAppService(res, &svcSpec, &infraSpec, svcConfig)
			if err != nil {
				return nil, err
			}

			err = mapHostUses(res, &svcSpec, backendMapping, existingMap, projectConfig)
			if err != nil {
				return nil, err
			}

			infraSpec.Services = append(infraSpec.Services, svcSpec)
		case ResourceTypeHostContainerApp:
			svcSpec := scaffold.ServiceSpec{
				Name: res.Name,
				Port: -1,
				Env:  map[string]string{},
				Host: scaffold.ContainerAppKind,
			}

			err := mapContainerApp(res, &svcSpec, &infraSpec)
			if err != nil {
				return nil, err
			}

			err = mapHostUses(res, &svcSpec, backendMapping, existingMap, projectConfig)
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
		case ResourceTypeMessagingEventHubs:
			if infraSpec.EventHubs != nil {
				return nil, fmt.Errorf("only one event hubs resource is currently allowed")
			}
			props := res.Props.(EventHubsProps)
			infraSpec.EventHubs = &scaffold.EventHubs{
				Hubs: props.Hubs,
			}
		case ResourceTypeMessagingServiceBus:
			if infraSpec.ServiceBus != nil {
				return nil, fmt.Errorf("only one service bus resource is currently allowed")
			}
			props := res.Props.(ServiceBusProps)
			infraSpec.ServiceBus = &scaffold.ServiceBus{
				Queues: props.Queues,
				Topics: props.Topics,
			}
		case ResourceTypeStorage:
			if infraSpec.StorageAccount != nil {
				return nil, fmt.Errorf("only one storage account resource is currently allowed")
			}
			props := res.Props.(StorageProps)
			infraSpec.StorageAccount = &scaffold.StorageAccount{
				Containers: props.Containers,
			}
		case ResourceTypeAiProject:
			// It's okay to forcefully panic here. The only way we would land here is that the marshal/unmarshal
			// in resources.go was not done right.
			props := res.Props.(AiFoundryModelProps)
			foundryName := res.Name
			var foundryModels []scaffold.AiFoundryModel
			foundrySpec := scaffold.AiFoundrySpec{
				Name: foundryName,
			}
			for _, model := range props.Models {
				foundryModels = append(foundryModels, scaffold.AiFoundryModel{
					AIModelModel: scaffold.AIModelModel{
						Name:    model.Name,
						Version: model.Version,
					},
					Format: model.Format,
					Sku: scaffold.AiFoundryModelSku{
						Name:      model.Sku.Name,
						UsageName: model.Sku.UsageName,
						Capacity:  model.Sku.Capacity,
					},
				})
			}
			foundrySpec.Models = foundryModels
			infraSpec.AiFoundryProject = &foundrySpec
		case ResourceTypeKeyVault:
			infraSpec.KeyVault = &scaffold.KeyVault{}
		case ResourceTypeAiSearch:
			infraSpec.AISearch = &scaffold.AISearch{}
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
	}

	slices.SortFunc(infraSpec.Services, func(a, b scaffold.ServiceSpec) int {
		return strings.Compare(a.Name, b.Name)
	})

	return &infraSpec, nil
}

// mergeDefaultEnvVars combines default environment variables with user-provided ones.
func mergeDefaultEnvVars(defaultEnv map[string]string, userEnv []ServiceEnvVar) []ServiceEnvVar {
	// Map to track which env vars are provided by the user
	userEnvMap := make(map[string]struct{}, len(userEnv))
	for _, env := range userEnv {
		userEnvMap[env.Name] = struct{}{}
	}

	combinedEnv := make([]ServiceEnvVar, 0, len(defaultEnv)+len(userEnv))

	// Add default env vars that aren't overridden
	for name, value := range defaultEnv {
		if _, overridden := userEnvMap[name]; !overridden {
			combinedEnv = append(combinedEnv, ServiceEnvVar{
				Name:  name,
				Value: value,
			})
		}
	}

	// Add user-provided env vars
	combinedEnv = append(combinedEnv, userEnv...)
	return combinedEnv
}

func mapHostProps(
	res *ResourceConfig,
	svcSpec *scaffold.ServiceSpec,
	infraSpec *scaffold.InfraSpec,
	port int,
	env []ServiceEnvVar,
) error {
	for _, envVar := range env {
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

	if port < 1 || port > 65535 {
		return fmt.Errorf("port value %d for host %s must be between 1 and 65535", port, res.Name)
	}

	svcSpec.Port = port
	return nil
}

func mapContainerApp(res *ResourceConfig, svcSpec *scaffold.ServiceSpec, infraSpec *scaffold.InfraSpec) error {
	props := res.Props.(ContainerAppProps)
	return mapHostProps(res, svcSpec, infraSpec, props.Port, props.Env)
}

func mapAppService(
	res *ResourceConfig,
	svcSpec *scaffold.ServiceSpec,
	infraSpec *scaffold.InfraSpec,
	svcConfig *ServiceConfig,
) error {
	props := res.Props.(AppServiceProps)

	if len(props.Runtime.Stack) == 0 {
		return fmt.Errorf("resources.%s.runtime.type is required", res.Name)
	}

	if len(props.Runtime.Version) == 0 {
		return fmt.Errorf("resources.%s.runtime.version is required", res.Name)
	}

	svcSpec.Runtime = &scaffold.RuntimeInfo{
		Type:    string(props.Runtime.Stack),
		Version: props.Runtime.Version,
	}
	svcSpec.StartupCommand = props.StartupCommand

	defaultEnv := map[string]string{
		"SCM_DO_BUILD_DURING_DEPLOYMENT": "true",
		"ENABLE_ORYX_BUILD":              "true",
	}
	// Language-specific environment variables
	if svcConfig.Language == ServiceLanguagePython {
		defaultEnv["PYTHON_ENABLE_GUNICORN_MULTIWORKERS"] = "true"
	}
	combinedEnv := mergeDefaultEnvVars(defaultEnv, props.Env)

	if err := mapHostProps(res, svcSpec, infraSpec, props.Port, combinedEnv); err != nil {
		return err
	}

	return nil
}

func mapHostUses(
	res *ResourceConfig,
	svcSpec *scaffold.ServiceSpec,
	backendMapping map[string]string,
	existingMap map[string]*scaffold.ExistingResource,
	prj *ProjectConfig) error {
	for _, use := range res.Uses {
		useRes, ok := prj.Resources[use]
		if !ok {
			return fmt.Errorf("resource %s uses %s, which does not exist", res.Name, use)
		}

		if useRes.Existing {
			resourceMeta, ok := scaffold.ResourceMetaFromType(useRes.Type.AzureResourceType())
			if !ok {
				return fmt.Errorf("resource type '%s' is not currently supported for existing", string(res.Type))
			}

			existingDecl := existingMap[use]

			emitEnv := EmitEnv{FuncMap: scaffold.BaseEmitBicepFuncMap(), ResourceVarName: existingDecl.Name}
			emitter := func(val *scaffold.ExpressionVar, results map[string]string) error {
				return emitVariable(emitEnv, val, results)
			}

			results, err := scaffold.EmitBicep(resourceMeta.Variables, emitter)
			if err != nil {
				return fmt.Errorf("emitting bicep bindings for '%s': %w", useRes.Name, err)
			}

			for key, value := range results {
				envKey := scaffold.EnvVarName(
					fmt.Sprintf("%s_%s", resourceMeta.StandardVarPrefix, environment.Key(use)),
					key)

				if envValue, exists := svcSpec.Env[envKey]; exists {
					panic(fmt.Sprintf(
						"env collision: env value %s already set to %s, cannot set to %s", envKey, envValue, value))
				}

				svcSpec.Env[envKey] = value
			}

			svcSpec.Existing = append(svcSpec.Existing, existingDecl)
			continue
		}

		switch useRes.Type {
		case ResourceTypeDbMongo:
			svcSpec.DbCosmosMongo = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeDbCosmos:
			svcSpec.DbCosmos = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeDbPostgres:
			svcSpec.DbPostgres = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeDbMySql:
			svcSpec.DbMySql = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeDbRedis:
			svcSpec.DbRedis = &scaffold.DatabaseReference{DatabaseName: useRes.Name}
		case ResourceTypeHostAppService,
			ResourceTypeHostContainerApp:
			if svcSpec.Frontend == nil {
				svcSpec.Frontend = &scaffold.Frontend{}
			}

			svcSpec.Frontend.Backends = append(svcSpec.Frontend.Backends,
				scaffold.ServiceReference{Name: use})
			backendMapping[use] = res.Name // record the backend -> frontend mapping
		case ResourceTypeOpenAiModel:
			svcSpec.AIModels = append(svcSpec.AIModels, scaffold.AIModelReference{Name: use})
		case ResourceTypeMessagingEventHubs:
			svcSpec.EventHubs = &scaffold.EventHubs{}
		case ResourceTypeMessagingServiceBus:
			svcSpec.ServiceBus = &scaffold.ServiceBus{}
		case ResourceTypeStorage:
			svcSpec.StorageAccount = &scaffold.StorageReference{}
		case ResourceTypeAiProject:
			svcSpec.AiFoundryProject = &scaffold.AiFoundrySpec{}
		case ResourceTypeAiSearch:
			svcSpec.AISearch = &scaffold.AISearchReference{}
		case ResourceTypeKeyVault:
			svcSpec.KeyVault = &scaffold.KeyVaultReference{}
		}
	}

	return nil
}

type EmitEnv struct {
	// The function map to use for evaluating expressions.
	FuncMap scaffold.FuncMap

	// ResourceVarName is the name of Bicep symbol to assign property expressions.
	ResourceVarName string
}

func emitVariable(emitEnv EmitEnv, val *scaffold.ExpressionVar, results map[string]string) error {
	if len(val.Expressions) == 0 { // literal value, surround with quotes
		val.Value = fmt.Sprintf("'%s'", val.Value)
		return nil
	}

	// by default, surround each expression with ${} within a Bicep interpolated string
	surround := func(s string) string {
		return fmt.Sprintf("${%s}", s)
	}

	// when the expression is a single expression that covers the entire value, don't surround it
	if len(val.Expressions) == 1 &&
		val.Expressions[0].Start == 0 &&
		val.Expressions[0].End == len(val.Value) {
		surround = func(s string) string {
			return s
		}
	}

	for _, expr := range val.Expressions {
		err := emitVariableExpression(emitEnv, val.Key, expr, surround, results)
		if err != nil {
			return fmt.Errorf("evaluating expression '%s': %w", val.Key, err)
		}
	}

	if isBicepInterpolatedString(val.Value) {
		// If the final value contains any interpolation ${}, we wrap the final value with quotes.
		//
		// By doing this "reflection-like" behavior of examining the output string,
		// we allow functions to be composable while respecting string-interpolation rules.
		//
		// Regardless of whether an interpolation was emitted as part of a function expression,
		// or because we surrounded values here, we will detect all cases and wrap the final value nicely.
		val.Value = fmt.Sprintf("'%s'", val.Value)
	}

	return nil
}

func emitVariableExpression(
	env EmitEnv,
	key string,
	expr *scaffold.Expression,
	surround func(string) string,
	results map[string]string) error {
	switch expr.Kind {
	case scaffold.PropertyExpr:
		path := expr.Data.(scaffold.PropertyExprData).PropertyPath
		expr.Replace(surround(fmt.Sprintf("%s.%s", env.ResourceVarName, path)))
	case scaffold.VarExpr:
		name := expr.Data.(scaffold.VarExprData).Name
		expr.Replace(surround(results[name]))
	case scaffold.FuncExpr:
		funcData := expr.Data.(scaffold.FuncExprData)
		funcName := funcData.FuncName

		// Check if function exists
		fn, ok := env.FuncMap[funcName]
		if !ok {
			return fmt.Errorf("unknown function: %s", funcName)
		}

		// for arguments of a function, we return the value as literal.
		// the interpolation (if any) is done in the function itself.
		id := func(s string) string { return s }

		// Evaluate all arguments
		args := make([]interface{}, 0, len(funcData.Args))
		for _, arg := range funcData.Args {
			err := emitVariableExpression(env, key, arg, id, results)
			if err != nil {
				return fmt.Errorf("evaluating arguments for '%s': %w", funcName, err)
			}

			args = append(args, arg.Value)
		}

		// Call the function
		funcResult, err := scaffold.CallFn(fn, funcName, args)
		if err != nil {
			return fmt.Errorf("calling '%s' failed: %w", funcName, err)
		}

		resultString := fmt.Sprintf("%v", funcResult)
		expr.Replace(surround(resultString))
	case scaffold.SpecExpr:
		return fmt.Errorf("spec expressions are not currently supported in existing resources")
	case scaffold.VaultExpr:
		return fmt.Errorf("vault expressions are not currently supported in existing resources")
	}

	return nil
}

// isBicepInterpolatedString checks if a string contains any Bicep interpolation expressions.
//
// Bicep interpolation expressions are of the form ${expression},
// and are not escaped with a backslash.
func isBicepInterpolatedString(s string) bool {
	for i, r := range s {
		if r == '$' &&
			i+1 < len(s) && s[i+1] == '{' && // we see '${'
			i-1 >= 0 && s[i-1] != '\\' { // we do not see escaped '\${'
			return true
		}
	}
	return false
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

// DependentResourcesOf returns implicit resource dependencies for a given resource type.
// These dependencies (like Key Vault to store connection strings, passwords for databases)
// are automatically added to the project configuration. Returns an empty slice if none exist.
func DependentResourcesOf(resource *ResourceConfig) []*ResourceConfig {
	switch resource.Type {
	case ResourceTypeDbMongo, ResourceTypeDbMySql, ResourceTypeDbPostgres, ResourceTypeDbRedis:
		return []*ResourceConfig{{Name: "vault", Type: ResourceTypeKeyVault}}
	default:
		return nil
	}
}
