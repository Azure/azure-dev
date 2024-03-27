package apphost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/psanford/memfs"
	"golang.org/x/exp/maps"
	"gopkg.in/yaml.v3"
)

const RedisContainerAppService = "redis"

const DaprStateStoreComponentType = "state"
const DaprPubSubComponentType = "pubsub"

// genTemplates is the collection of templates that are used when generating infrastructure files from a manifest.
var genTemplates *template.Template

func init() {
	tmpl, err := template.New("templates").
		Option("missingkey=error").
		Funcs(
			template.FuncMap{
				"bicepName":              scaffold.BicepName,
				"alphaSnakeUpper":        scaffold.AlphaSnakeUpper,
				"containerAppName":       scaffold.ContainerAppName,
				"containerAppSecretName": scaffold.ContainerAppSecretName,
				"fixBackSlash": func(src string) string {
					return strings.ReplaceAll(src, "\\", "/")
				},
				"dashToUnderscore": func(src string) string {
					return strings.ReplaceAll(src, "-", "_")
				},
				"envFormat": scaffold.EnvFormat,
			},
		).
		ParseFS(resources.AppHostTemplates, "apphost/templates/*")
	if err != nil {
		panic("failed to parse generator templates: " + err.Error())
	}

	genTemplates = tmpl
}

type ContentsAndMode struct {
	Contents string
	Mode     fs.FileMode
}

// ProjectPaths returns a map of project names to their paths.
func ProjectPaths(manifest *Manifest) map[string]string {
	res := make(map[string]string)

	for name, comp := range manifest.Resources {
		switch comp.Type {
		case "project.v0":
			res[name] = *comp.Path
		}
	}

	return res
}

// Dockerfiles returns information about all dockerfile.v0 resources from a manifest.
func Dockerfiles(manifest *Manifest) map[string]genDockerfile {
	res := make(map[string]genDockerfile)

	for name, comp := range manifest.Resources {
		switch comp.Type {
		case "dockerfile.v0":
			res[name] = genDockerfile{
				Path:      *comp.Path,
				Context:   *comp.Context,
				Env:       comp.Env,
				Bindings:  comp.Bindings,
				BuildArgs: comp.BuildArgs,
			}
		}
	}

	return res
}

// ContainerAppManifestTemplateForProject returns the container app manifest template for a given project.
// It can be used (after evaluation) to deploy the service to a container app environment.
func ContainerAppManifestTemplateForProject(
	manifest *Manifest, projectName string) (string, error) {
	generator := newInfraGenerator()

	if err := generator.LoadManifest(manifest); err != nil {
		return "", err
	}

	if err := generator.Compile(); err != nil {
		return "", err
	}

	var buf bytes.Buffer

	err := genTemplates.ExecuteTemplate(&buf, "containerApp.tmpl.yaml", generator.containerAppTemplateContexts[projectName])
	if err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}

// BicepTemplate returns a filesystem containing the generated bicep files for the given manifest. These files represent
// the shared infrastructure that would normally be under the `infra/` folder for the given manifest.
func BicepTemplate(manifest *Manifest) (*memfs.FS, error) {
	generator := newInfraGenerator()

	if err := generator.LoadManifest(manifest); err != nil {
		return nil, err
	}

	if err := generator.Compile(); err != nil {
		return nil, err
	}

	// use the filesystem coming from the manifest
	// the in-memory filesystem from the manifest is guaranteed to be initialized and contains all the bicep files
	// referenced by the Aspire manifest.
	fs := manifest.BicepFiles

	// bicepContext merges the bicepContext with the inputs from the manifest to execute the main.bicep template
	// this allows the template to access the auto-gen inputs from the generator
	type genInput struct {
		Name   string
		Secret bool
		Type   string
	}
	type autoGenInput struct {
		genInput
		MetadataConfig string
		MetadataType   azure.AzdMetadataType
	}
	type bicepContext struct {
		genBicepTemplateContext
		WithMetadataParameters []autoGenInput
		MainToResourcesParams  []genInput
	}
	var parameters []autoGenInput
	var mapToResourceParams []genInput

	// order to be deterministic when writing bicep
	genParametersKeys := maps.Keys(generator.bicepContext.InputParameters)
	slices.Sort(genParametersKeys)

	for _, key := range genParametersKeys {
		parameter := generator.bicepContext.InputParameters[key]
		parameterMetadata := ""
		if parameter.Default != nil && parameter.Default.Generate != nil {
			pMetadata, err := inputMetadata(*parameter.Default.Generate)
			if err != nil {
				return nil, fmt.Errorf("generating input metadata for %s: %w", key, err)
			}
			parameterMetadata = pMetadata
		}
		input := genInput{Name: key, Secret: parameter.Secret, Type: parameter.Type}
		parameters = append(parameters, autoGenInput{
			genInput:       input,
			MetadataConfig: parameterMetadata,
			MetadataType:   azure.AzdMetadataTypeGenerate})
		if slices.Contains(generator.bicepContext.mappedParameters, strings.ReplaceAll(key, "-", "_")) {
			mapToResourceParams = append(mapToResourceParams, input)
		}
	}
	context := bicepContext{
		genBicepTemplateContext: generator.bicepContext,
		WithMetadataParameters:  parameters,
		MainToResourcesParams:   mapToResourceParams,
	}
	if err := executeToFS(fs, genTemplates, "main.bicep", "main.bicep", context); err != nil {
		return nil, fmt.Errorf("generating infra/main.bicep: %w", err)
	}

	if err := executeToFS(fs, genTemplates, "resources.bicep", "resources.bicep", context); err != nil {
		return nil, fmt.Errorf("generating infra/resources.bicep: %w", err)
	}

	if err := executeToFS(
		fs, genTemplates, "main.parameters.json", "main.parameters.json", generator.bicepContext); err != nil {
		return nil, fmt.Errorf("generating infra/resources.bicep: %w", err)
	}

	return fs, nil
}

func inputMetadata(config InputDefaultGenerate) (string, error) {
	finalLength := convert.ToValueWithDefault(config.MinLength, 0)
	clusterLength := convert.ToValueWithDefault(config.MinLower, 0) +
		convert.ToValueWithDefault(config.MinUpper, 0) +
		convert.ToValueWithDefault(config.MinNumeric, 0) +
		convert.ToValueWithDefault(config.MinSpecial, 0)
	if clusterLength > finalLength {
		finalLength = clusterLength
	}

	adaptBool := func(b *bool) *bool {
		if b == nil {
			return b
		}
		return to.Ptr(!*b)
	}

	metadataModel := azure.AutoGenInput{
		Length:     finalLength,
		MinLower:   config.MinLower,
		MinUpper:   config.MinUpper,
		MinNumeric: config.MinNumeric,
		MinSpecial: config.MinSpecial,
		NoLower:    adaptBool(config.Lower),
		NoUpper:    adaptBool(config.Upper),
		NoNumeric:  adaptBool(config.Numeric),
		NoSpecial:  adaptBool(config.Special),
	}

	metadataBytes, err := json.Marshal(metadataModel)
	if err != nil {
		return "", fmt.Errorf("marshalling metadata: %w", err)
	}

	// key identifiers for objects on bicep don't need quotes, unless they have special characters, like `-` or `.`.
	// jsonSimpleKeyRegex is used to remove the quotes from the key if no needed to avoid a bicep lint warning.
	return jsonSimpleKeyRegex.ReplaceAllString(string(metadataBytes), "${1}:"), nil
}

// GenerateProjectArtifacts generates all the artifacts to manage a project with `azd`. The azure.yaml file as well as
// a helpful next-steps.md file.
func GenerateProjectArtifacts(
	ctx context.Context,
	projectDir string,
	projectName string,
	manifest *Manifest,
	appHostProject string,
) (map[string]ContentsAndMode, error) {
	appHostRel, err := filepath.Rel(projectDir, appHostProject)
	if err != nil {
		return nil, err
	}

	generator := newInfraGenerator()

	if err := generator.LoadManifest(manifest); err != nil {
		return nil, err
	}

	if err := generator.Compile(); err != nil {
		return nil, err
	}

	generatedFS := memfs.New()

	projectFileContext := genProjectFileContext{
		Name: projectName,
		Services: map[string]string{
			"app": fmt.Sprintf(".%s%s", string(filepath.Separator), appHostRel),
		},
	}

	if err := executeToFS(generatedFS, genTemplates, "azure.yaml", "azure.yaml", projectFileContext); err != nil {
		return nil, fmt.Errorf("generating azure.yaml: %w", err)
	}

	if err := executeToFS(generatedFS, genTemplates, "next-steps.md", "next-steps.md", nil); err != nil {
		return nil, fmt.Errorf("generating next-steps.md: %w", err)
	}

	files := make(map[string]ContentsAndMode)

	err = fs.WalkDir(generatedFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		contents, err := fs.ReadFile(generatedFS, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}

		files[path] = ContentsAndMode{
			Contents: string(contents),
			Mode:     info.Mode(),
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

type infraGenerator struct {
	containers        map[string]genContainer
	dapr              map[string]genDapr
	dockerfiles       map[string]genDockerfile
	projects          map[string]genProject
	connectionStrings map[string]string
	// keeps the value from value.v0 resources if provided.
	valueStrings  map[string]string
	resourceTypes map[string]string

	bicepContext                 genBicepTemplateContext
	containerAppTemplateContexts map[string]genContainerAppManifestTemplateContext
}

func newInfraGenerator() *infraGenerator {
	return &infraGenerator{
		bicepContext: genBicepTemplateContext{
			AppInsights:                     make(map[string]genAppInsight),
			ContainerAppEnvironmentServices: make(map[string]genContainerAppEnvironmentServices),
			ServiceBuses:                    make(map[string]genServiceBus),
			StorageAccounts:                 make(map[string]genStorageAccount),
			KeyVaults:                       make(map[string]genKeyVault),
			ContainerApps:                   make(map[string]genContainerApp),
			AppConfigs:                      make(map[string]genAppConfig),
			DaprComponents:                  make(map[string]genDaprComponent),
			CosmosDbAccounts:                make(map[string]genCosmosAccount),
			SqlServers:                      make(map[string]genSqlServer),
			InputParameters:                 make(map[string]Input),
			BicepModules:                    make(map[string]genBicepModules),
			OutputParameters:                make(map[string]genOutputParameter),
			OutputSecretParameters:          make(map[string]genOutputParameter),
		},
		containers:                   make(map[string]genContainer),
		dapr:                         make(map[string]genDapr),
		dockerfiles:                  make(map[string]genDockerfile),
		projects:                     make(map[string]genProject),
		connectionStrings:            make(map[string]string),
		resourceTypes:                make(map[string]string),
		containerAppTemplateContexts: make(map[string]genContainerAppManifestTemplateContext),
	}
}

// withOutputsExpRegex is a regular expression used to match expressions in the format "{<resource>.outputs.<outputName>}" or
// "{<resource>.secretOutputs.<outputName>}".
var withOutputsExpRegex = regexp.MustCompile(`\{[a-zA-Z0-9\-]+\.(outputs|secretOutputs)\.[a-zA-Z0-9\-]+\}`)

// evaluateForOutputs is a function that evaluates a given value and extracts output parameters from it.
// It searches for patterns in the form of "{<resource>.outputs.<outputName>}" or "{<resource>.secretOutputs.<outputName>}"
// and creates a map of output parameters with their corresponding values.
// The output parameter names are generated by concatenating the uppercase versions of the resource and output names,
// separated by an underscore.
// The function returns the map of output parameters and an error, if any.
func evaluateForOutputs(value string) (map[string]genOutputParameter, error) {
	outputs := make(map[string]genOutputParameter)

	matches := withOutputsExpRegex.FindAllString(value, -1)
	for _, match := range matches {
		noBrackets := strings.TrimRight(strings.TrimLeft(match, "{"), "}")
		parts := strings.Split(noBrackets, ".")
		name := fmt.Sprintf("%s_%s", strings.ToUpper(parts[0]), strings.ToUpper(parts[2]))
		outputs[name] = genOutputParameter{
			Type:  "string",
			Value: noBrackets,
		}
	}
	return outputs, nil
}

// extractOutputs evaluates the known fields where a Resource can reference an output and persist it.
func (b *infraGenerator) extractOutputs(resource *Resource) error {
	// from connection string
	if resource.ConnectionString != nil {
		outputs, err := evaluateForOutputs(*resource.ConnectionString)
		if err != nil {
			return err
		}
		for key, output := range outputs {
			if strings.Contains(output.Value, ".outputs.") {
				b.bicepContext.OutputParameters[key] = output
			} else {
				b.bicepContext.OutputSecretParameters[key] = output
			}
		}
	}
	for _, value := range resource.Env {
		outputs, err := evaluateForOutputs(value)
		if err != nil {
			return err
		}
		for key, output := range outputs {
			if strings.Contains(output.Value, ".outputs.") {
				b.bicepContext.OutputParameters[key] = output
			} else {
				b.bicepContext.OutputSecretParameters[key] = output
			}
		}

	}
	return nil
}

// LoadManifest loads the given manifest into the generator. It should be called before [Compile].
func (b *infraGenerator) LoadManifest(m *Manifest) error {
	for name, comp := range m.Resources {
		if err := b.extractOutputs(comp); err != nil {
			return fmt.Errorf("extracting outputs: %w", err)
		}

		b.resourceTypes[name] = comp.Type

		if comp.ConnectionString != nil {
			b.connectionStrings[name] = *comp.ConnectionString
		}

		switch comp.Type {
		case "azure.servicebus.v0":
			b.addServiceBus(name, comp.Queues, comp.Topics)
		case "azure.appinsights.v0":
			b.addAppInsights(name)
		case "project.v0":
			b.addProject(name, *comp.Path, comp.Env, comp.Bindings)
		case "container.v0":
			b.addContainer(name, *comp.Image, comp.Env, comp.Bindings, comp.Inputs, comp.Volumes)
		case "dapr.v0":
			err := b.addDapr(name, comp.Dapr)
			if err != nil {
				return err
			}
		case "dapr.component.v0":
			err := b.addDaprComponent(name, comp.DaprComponent)
			if err != nil {
				return err
			}
		case "dockerfile.v0":
			b.addDockerfile(name, *comp.Path, *comp.Context, comp.Env, comp.Bindings, comp.BuildArgs)
		case "redis.v0":
			b.addContainerAppService(name, RedisContainerAppService)
		case "azure.keyvault.v0":
			b.addKeyVault(name, false, false)
		case "azure.appconfiguration.v0":
			b.addAppConfig(name)
		case "azure.storage.v0":
			b.addStorageAccount(name)
		case "azure.storage.blob.v0":
			b.addStorageBlob(*comp.Parent, name)
		case "azure.storage.queue.v0":
			b.addStorageQueue(*comp.Parent, name)
		case "azure.storage.table.v0":
			b.addStorageTable(*comp.Parent, name)
		case "azure.cosmosdb.account.v0":
			b.addCosmosDbAccount(name)
		case "azure.cosmosdb.database.v0":
			b.addCosmosDatabase(*comp.Parent, name)
		case "azure.sql.v0", "sqlserver.server.v0":
			b.addSqlServer(name)
		case "azure.sql.database.v0", "sqlserver.database.v0":
			if comp.Parent == nil || *comp.Parent == "" {
				return fmt.Errorf("database resource %s does not have a parent", name)
			}
			if m.Resources[*comp.Parent].Type != "container.v0" {
				// When the resource has a server (using container) as a parent, it means that the database is
				// NOT created within AzureSql service, and db will use parent's connection string instead.
				b.addSqlDatabase(*comp.Parent, name)
			}
		case "postgres.server.v0":
			b.addContainerAppService(name, "postgres")
		case "postgres.database.v0":
			if comp.Parent == nil || *comp.Parent == "" {
				return fmt.Errorf("database resource %s does not have a parent", name)
			}
			pType := m.Resources[*comp.Parent].Type
			if pType != "container.v0" && pType != "postgres.server.v0" {
				// When the resource has a server (container or p.server) as a parent, it means that the database is
				// a part of a server and it should not be created as a separate resource.
				b.addContainerAppService(name, "postgres")
			}
		case "parameter.v0":
			if err := b.addInputParameter(name, comp); err != nil {
				return fmt.Errorf("adding bicep parameter from resource %s (%s): %w", name, comp.Type, err)
			}
		case "value.v0":
			if comp.Value != "" && !hasInputs(comp.Value) {
				// a value.v0 resource with value not referencing inputs doesn't need any further processing
				b.valueStrings[name] = comp.Value
				continue
			}
			if err := b.addInputParameter(name, comp); err != nil {
				return fmt.Errorf("adding bicep parameter from resource %s (%s): %w", name, comp.Type, err)
			}
		case "azure.bicep.v0":
			if err := b.addBicep(name, comp); err != nil {
				return fmt.Errorf("adding bicep resource %s: %w", name, err)
			}
		default:
			ignore, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES"))
			if err == nil && ignore {
				log.Printf(
					"ignoring resource of type %s since AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES is set",
					comp.Type)
				continue
			}
			return fmt.Errorf("unsupported resource type: %s", comp.Type)
		}
	}

	return nil
}

func (b *infraGenerator) requireCluster() {
	b.requireLogAnalyticsWorkspace()
	b.bicepContext.HasContainerEnvironment = true
}

func (b *infraGenerator) requireContainerRegistry() {
	b.bicepContext.HasContainerRegistry = true
}

func (b *infraGenerator) requireDaprStore() string {
	daprStoreName := "daprStore"

	if !b.bicepContext.HasDaprStore {
		b.requireCluster()

		// A single store can be shared across all Dapr components, so we only need to create one.
		b.addContainerAppService(daprStoreName, RedisContainerAppService)

		b.bicepContext.HasDaprStore = true
	}

	return daprStoreName
}

func (b *infraGenerator) requireLogAnalyticsWorkspace() {
	b.bicepContext.HasLogAnalyticsWorkspace = true
}

func (b *infraGenerator) requireStorageVolume() {
	b.bicepContext.RequiresStorageVolume = true
}

func (b *infraGenerator) addServiceBus(name string, queues, topics *[]string) {
	if queues == nil {
		queues = &[]string{}
	}

	if topics == nil {
		topics = &[]string{}
	}
	b.bicepContext.ServiceBuses[name] = genServiceBus{Queues: *queues, Topics: *topics}
}

func (b *infraGenerator) addInputParameter(name string, comp *Resource) error {
	pValue := comp.Value

	if !hasInputs(pValue) {
		// no inputs in the value, nothing to do
		return nil
	}

	input, err := resolveResourceInput(name, comp)
	if err != nil {
		return fmt.Errorf("resolving input for parameter %s: %w", name, err)
	}

	b.bicepContext.InputParameters[name] = input
	return nil
}

func hasInputs(value string) bool {
	matched, _ := regexp.MatchString(`{[a-zA-Z][a-zA-Z0-9\-]*\.inputs\.[a-zA-Z][a-zA-Z0-9\-]*}`, value)
	return matched
}

func resolveResourceInput(fromResource string, comp *Resource) (Input, error) {
	value := comp.Value

	valueParts := strings.Split(
		strings.TrimRight(strings.TrimLeft(value, "{"), "}"),
		".inputs.")
	// regex from above ensure parts 0 and 1 exists
	resourceName, inputName := valueParts[0], valueParts[1]

	if fromResource != resourceName {
		return Input{}, fmt.Errorf(
			"parameter %s does not use inputs from its own resource. This is not supported", fromResource)
	}

	input, exists := comp.Inputs[inputName]
	if !exists {
		return Input{}, fmt.Errorf("parameter %s does not have input %s", fromResource, inputName)
	}
	if input.Type == "" {
		input.Type = "string"
	}
	return input, nil
}

func (b *infraGenerator) addBicep(name string, comp *Resource) error {
	if comp.Path == nil {
		if comp.Parent == nil {
			return fmt.Errorf("bicep resource %s does not have a path or a parent", name)
		}
		// module uses parent
		return nil
	}
	if comp.Params == nil {
		comp.Params = make(map[string]any)
	}

	// afterInjectionParams is used to know which params where actually injected
	autoInjectedParams := make(map[string]any)
	// params from resource are type-less (any), injectValueForBicepParameter() will convert them to string
	// by converting to string, we can evaluate arrays and objects with placeholders.
	stringParams := make(map[string]string)
	for p, pVal := range comp.Params {
		paramValue, injected, err := injectValueForBicepParameter(name, p, pVal)
		if err != nil {
			return fmt.Errorf("injecting value for bicep parameter %s: %w", p, err)
		}
		stringParams[p] = paramValue
		if injected {
			autoInjectedParams[p] = struct{}{}
		}
	}
	if _, keyVaultInjected := autoInjectedParams[knownParameterKeyVault]; keyVaultInjected {
		b.addKeyVault(name+"kv", true, true)
	}

	b.bicepContext.BicepModules[name] = genBicepModules{Path: *comp.Path, Params: stringParams}
	return nil
}

const (
	knownParameterKeyVault      string = "keyVaultName"
	knownParameterPrincipalId   string = "principalId"
	knownParameterPrincipalType string = "principalType"
	knownParameterPrincipalName string = "principalName"
	knownParameterLogAnalytics  string = "logAnalyticsWorkspaceId"

	knownInjectedValuePrincipalId   string = "resources.outputs.MANAGED_IDENTITY_PRINCIPAL_ID"
	knownInjectedValuePrincipalType string = "'ServicePrincipal'"
	knownInjectedValuePrincipalName string = "resources.outputs.MANAGED_IDENTITY_NAME"
	knownInjectedValueLogAnalytics  string = "resources.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_ID"
)

// injectValueForBicepParameter checks for aspire-manifest and azd conventions rules for auto injecting values for
// the bicep.v0 parameters.
// Conventions examples:
// - for a `keyVaultName` parameter, set the value to the output of the keyVault resource to be created.
// - for `principalName`, set the value to the managed identity created by azd.
// Note: The value is only injected when it is an empty string.
// injectValueForBicepParameter returns the final value for the parameter, a boolean indicating if the value was injected
// and an error if any.
func injectValueForBicepParameter(resourceName, p string, parameter any) (string, bool, error) {
	// using json.Marshal to parse any type of value (array, bool, etc)
	jsonBytes, err := json.Marshal(parameter)
	if err != nil {
		return "", false, fmt.Errorf("marshalling param %s. error: %w", p, err)
	}
	finalParamValue := string(jsonBytes)
	emptyJsonString := "\"\""
	if finalParamValue != emptyJsonString {
		// injection not required
		return finalParamValue, false, nil
	}

	if p == knownParameterKeyVault {
		dashToUnderscore := strings.ReplaceAll(resourceName, "-", "_")
		return fmt.Sprintf("resources.outputs.SERVICE_BINDING_%s_NAME", strings.ToUpper(dashToUnderscore+"kv")), true, nil
	}
	if p == knownParameterPrincipalId {
		return knownInjectedValuePrincipalId, true, nil
	}
	if p == knownParameterPrincipalType {
		return knownInjectedValuePrincipalType, true, nil
	}
	if p == knownParameterPrincipalName {
		return knownInjectedValuePrincipalName, true, nil
	}
	if p == knownParameterLogAnalytics {
		return knownInjectedValueLogAnalytics, true, nil
	}
	return finalParamValue, false, nil
}

func (b *infraGenerator) addAppInsights(name string) {
	b.requireLogAnalyticsWorkspace()
	b.bicepContext.AppInsights[name] = genAppInsight{}
}

func (b *infraGenerator) addCosmosDbAccount(name string) {
	if _, exists := b.bicepContext.CosmosDbAccounts[name]; !exists {
		b.bicepContext.CosmosDbAccounts[name] = genCosmosAccount{}
	}
}

func (b *infraGenerator) addCosmosDatabase(cosmosDbAccount, dbName string) {
	account := b.bicepContext.CosmosDbAccounts[cosmosDbAccount]
	account.Databases = append(account.Databases, dbName)
	b.bicepContext.CosmosDbAccounts[cosmosDbAccount] = account
}

func (b *infraGenerator) addSqlServer(name string) {
	if _, exists := b.bicepContext.SqlServers[name]; !exists {
		b.bicepContext.SqlServers[name] = genSqlServer{}
	}
}

func (b *infraGenerator) addSqlDatabase(sqlAccount, dbName string) {
	account := b.bicepContext.SqlServers[sqlAccount]
	account.Databases = append(account.Databases, dbName)
	b.bicepContext.SqlServers[sqlAccount] = account
}

func (b *infraGenerator) addProject(
	name string, path string, env map[string]string, bindings map[string]*Binding,
) {
	b.requireCluster()
	b.requireContainerRegistry()

	b.projects[name] = genProject{
		Path:     path,
		Env:      env,
		Bindings: bindings,
	}
}

func (b *infraGenerator) addContainerAppService(name string, serviceType string) {
	b.requireCluster()

	b.bicepContext.ContainerAppEnvironmentServices[name] = genContainerAppEnvironmentServices{
		Type: serviceType,
	}
}

func (b *infraGenerator) addStorageAccount(name string) {
	// storage account can be added from addStorageTable, addStorageQueue or addStorageBlob
	// We only need to add it if it wasn't added before to cover cases of manifest with only one storage account and no
	// blobs, queues or tables.
	if _, exists := b.bicepContext.StorageAccounts[name]; !exists {
		b.bicepContext.StorageAccounts[name] = genStorageAccount{}
	}
}

func (b *infraGenerator) addKeyVault(name string, noTags, readAccessPrincipalId bool) {
	b.bicepContext.KeyVaults[name] = genKeyVault{
		NoTags:                noTags,
		ReadAccessPrincipalId: readAccessPrincipalId,
	}
}

func (b *infraGenerator) addAppConfig(name string) {
	b.bicepContext.AppConfigs[name] = genAppConfig{}
}

func (b *infraGenerator) addStorageBlob(storageAccount, blobName string) {
	account := b.bicepContext.StorageAccounts[storageAccount]
	account.Blobs = append(account.Blobs, blobName)
	b.bicepContext.StorageAccounts[storageAccount] = account
}

func (b *infraGenerator) addStorageQueue(storageAccount, queueName string) {
	account := b.bicepContext.StorageAccounts[storageAccount]
	account.Queues = append(account.Queues, queueName)
	b.bicepContext.StorageAccounts[storageAccount] = account
}

func (b *infraGenerator) addStorageTable(storageAccount, tableName string) {
	account := b.bicepContext.StorageAccounts[storageAccount]
	account.Tables = append(account.Tables, tableName)
	b.bicepContext.StorageAccounts[storageAccount] = account
}

func (b *infraGenerator) addContainer(
	name string,
	image string,
	env map[string]string,
	bindings map[string]*Binding,
	inputs map[string]Input,
	volumes []*Volume) {
	b.requireCluster()

	if len(volumes) > 0 {
		b.requireStorageVolume()
	}

	b.containers[name] = genContainer{
		Image:    image,
		Env:      env,
		Bindings: bindings,
		Inputs:   inputs,
		Volumes:  volumes,
	}
}

func (b *infraGenerator) addDapr(name string, metadata *DaprResourceMetadata) error {
	if metadata == nil || metadata.Application == nil || metadata.AppId == nil {
		return fmt.Errorf("dapr resource '%s' did not include required metadata", name)
	}

	b.requireCluster()

	// NOTE: ACA only supports a small subset of the Dapr sidecar configuration options.
	b.dapr[name] = genDapr{
		AppId:                  *metadata.AppId,
		Application:            *metadata.Application,
		AppPort:                metadata.AppPort,
		AppProtocol:            metadata.AppProtocol,
		DaprHttpMaxRequestSize: metadata.DaprHttpMaxRequestSize,
		DaprHttpReadBufferSize: metadata.DaprHttpReadBufferSize,
		EnableApiLogging:       metadata.EnableApiLogging,
		LogLevel:               metadata.LogLevel,
	}

	return nil
}

func (b *infraGenerator) addDaprComponent(name string, metadata *DaprComponentResourceMetadata) error {
	if metadata == nil || metadata.Type == nil {
		return fmt.Errorf("dapr component resource '%s' did not include required metadata", name)
	}

	switch *metadata.Type {
	case DaprPubSubComponentType:
		b.addDaprPubSubComponent(name)
	case DaprStateStoreComponentType:
		b.addDaprStateStoreComponent(name)
	default:
		return fmt.Errorf("dapr component resource '%s' has unsupported type '%s'", name, *metadata.Type)
	}

	return nil
}

func (b *infraGenerator) addDaprRedisComponent(componentName string, componentType string) {
	redisName := b.requireDaprStore()

	component := genDaprComponent{
		Metadata: make(map[string]genDaprComponentMetadata),
		Secrets:  make(map[string]genDaprComponentSecret),
		Type:     fmt.Sprintf("%s.redis", componentType),
		Version:  "v1",
	}

	redisPort := 6379

	// The Redis component expects the host to be in the format <host>:<port>.
	// NOTE: the "short name" should suffice rather than the FQDN.
	redisHost := fmt.Sprintf(`'${%s.name}:%d'`, redisName, redisPort)

	// The Redis add-on exposes its configuration as an ACA secret with the form:
	//   'requirepass <128 character password>dir ...'
	//
	// We need to extract the password from this secret and pass it to the Redis component.
	// While apps could "service bind" to the Redis add-on, in which case the password would be
	// available as an environment variable, the Dapr environment variable secret store is not
	// currently available in ACA.
	redisPassword := fmt.Sprintf(`substring(%s.listSecrets().value[0].value, 12, 128)`, redisName)

	redisPasswordKey := "password"
	redisSecretKeyRef := fmt.Sprintf(`'%s'`, redisPasswordKey)

	component.Metadata["redisHost"] = genDaprComponentMetadata{
		Value: &redisHost,
	}

	//
	// Create a secret for the Redis password. This secret will be then be referenced by the component metadata.
	//

	component.Metadata["redisPassword"] = genDaprComponentMetadata{
		SecretKeyRef: &redisSecretKeyRef,
	}

	component.Secrets[redisPasswordKey] = genDaprComponentSecret{
		Value: redisPassword,
	}

	b.bicepContext.DaprComponents[componentName] = component
}

func (b *infraGenerator) addDaprPubSubComponent(name string) {
	b.addDaprRedisComponent(name, DaprPubSubComponentType)
}

func (b *infraGenerator) addDaprStateStoreComponent(name string) {
	b.addDaprRedisComponent(name, DaprStateStoreComponentType)
}

func (b *infraGenerator) addDockerfile(
	name string, path string, context string, env map[string]string,
	bindings map[string]*Binding, buildArgs map[string]string,
) {
	b.requireCluster()
	b.requireContainerRegistry()

	b.dockerfiles[name] = genDockerfile{
		Path:      path,
		Context:   context,
		Env:       env,
		Bindings:  bindings,
		BuildArgs: buildArgs,
	}
}

func validateAndMergeBindings(bindings map[string]*Binding) (*Binding, error) {
	if len(bindings) == 0 {
		return nil, nil
	}

	if len(bindings) == 1 {
		for _, binding := range bindings {
			return binding, nil
		}
	}

	var validatedBinding *Binding

	for _, binding := range bindings {
		if validatedBinding == nil {
			validatedBinding = binding
			continue
		}

		if validatedBinding.External != binding.External {
			return nil, fmt.Errorf("the external property of all bindings should match")
		}

		if validatedBinding.Transport != binding.Transport {
			return nil, fmt.Errorf("the transport property of all bindings should match")
		}

		if validatedBinding.Protocol != binding.Protocol {
			return nil, fmt.Errorf("the protocol property of all bindings should match")
		}

		if validatedBinding.Protocol != binding.Protocol {
			return nil, fmt.Errorf("the protocol property of all bindings should match")
		}

		if (validatedBinding.ContainerPort == nil && binding.ContainerPort != nil) ||
			(validatedBinding.ContainerPort != nil && binding.ContainerPort == nil) {
			return nil, fmt.Errorf("the container port property of all bindings should match")
		}

		if validatedBinding.ContainerPort != nil && binding.ContainerPort != nil &&
			*validatedBinding.ContainerPort != *binding.ContainerPort {
			return nil, fmt.Errorf("the container port property of all bindings should match")
		}
	}

	return validatedBinding, nil
}

// singleQuotedStringRegex is a regular expression pattern used to match single-quoted strings.
var singleQuotedStringRegex = regexp.MustCompile(`'[^']*'`)
var propertyNameRegex = regexp.MustCompile(`'([^']*)':`)
var jsonSimpleKeyRegex = regexp.MustCompile(`"([a-zA-Z0-9]*)":`)

// Compile compiles the loaded manifest into the internal representation used to generate the infrastructure files. Once
// called the context objects on the infraGenerator can be passed to the text templates to generate the required
// infrastructure.
func (b *infraGenerator) Compile() error {
	for name, container := range b.containers {
		cs := genContainerApp{
			Image:   container.Image,
			Env:     make(map[string]string),
			Secrets: make(map[string]string),
			Volumes: container.Volumes,
		}

		ingress, err := buildIngress(container.Bindings)
		if err != nil {
			return fmt.Errorf("configuring ingress for resource %s: %w", name, err)
		}

		cs.Ingress = ingress
		parameters := maps.Keys(b.bicepContext.InputParameters)
		for i, parameter := range parameters {
			parameters[i] = strings.ReplaceAll(parameter, "-", "_")
		}
		for k, value := range container.Env {
			// first evaluation using inputEmitTypeYaml to know if there are secrets on the value
			yamlString, err := EvalString(
				value, func(s string) (string, error) { return b.evalBindingRef(s, inputEmitTypeYaml) })
			if err != nil {
				return fmt.Errorf("configuring environment for resource %s: evaluating value for %s: %w", name, k, err)
			}
			// second evaluation to build the appropriate string for bicep, which only replaces parameter names
			// without caring about secret or not
			bicepString, err := EvalString(
				value, func(s string) (string, error) { return b.evalBindingRef(s, inputEmitTypeBicep) })
			if err != nil {
				return fmt.Errorf("configuring environment for resource %s: evaluating value for %s: %w", name, k, err)
			}
			if isComplexExp, val := isComplexExpression(fmt.Sprintf("'%s'", bicepString)); !isComplexExp {
				bicepString = val
			}

			if slices.Contains(parameters, bicepString) {
				if !slices.Contains(b.bicepContext.mappedParameters, bicepString) {
					b.bicepContext.mappedParameters = append(b.bicepContext.mappedParameters, bicepString)
				}
			}

			if strings.Contains(yamlString, "{{ securedParameter ") {
				cs.Secrets[k] = bicepString
			} else {
				cs.Env[k] = bicepString
			}
		}

		b.bicepContext.ContainerApps[name] = cs
	}

	for resourceName, docker := range b.dockerfiles {
		projectTemplateCtx := genContainerAppManifestTemplateContext{
			Name:            resourceName,
			Env:             make(map[string]string),
			Secrets:         make(map[string]string),
			KeyVaultSecrets: make(map[string]string),
		}

		ingress, err := buildIngress(docker.Bindings)
		if err != nil {
			return fmt.Errorf("configuring ingress for resource %s: %w", resourceName, err)
		}

		projectTemplateCtx.Ingress = ingress

		if err := b.buildEnvBlock(docker.Env, &projectTemplateCtx); err != nil {
			return fmt.Errorf("configuring environment for resource %s: %w", resourceName, err)
		}

		b.containerAppTemplateContexts[resourceName] = projectTemplateCtx
	}

	for resourceName, project := range b.projects {
		projectTemplateCtx := genContainerAppManifestTemplateContext{
			Name:            resourceName,
			Env:             make(map[string]string),
			Secrets:         make(map[string]string),
			KeyVaultSecrets: make(map[string]string),
		}

		binding, err := validateAndMergeBindings(project.Bindings)
		if err != nil {
			return fmt.Errorf("configuring ingress for project %s: %w", resourceName, err)
		}

		if binding != nil {
			projectTemplateCtx.Ingress = &genContainerAppIngress{
				External:  binding.External,
				Transport: binding.Transport,
				// This port number is for dapr
				TargetPort:    8080,
				AllowInsecure: strings.ToLower(binding.Transport) == "http2" || !binding.External,
			}
		}

		for _, dapr := range b.dapr {
			if dapr.Application == resourceName {
				appPort := dapr.AppPort

				if appPort == nil && projectTemplateCtx.Ingress != nil {
					appPort = &projectTemplateCtx.Ingress.TargetPort
				}

				projectTemplateCtx.Dapr = &genContainerAppManifestTemplateContextDapr{
					AppId:              dapr.AppId,
					AppPort:            appPort,
					AppProtocol:        dapr.AppProtocol,
					EnableApiLogging:   dapr.EnableApiLogging,
					HttpMaxRequestSize: dapr.DaprHttpMaxRequestSize,
					HttpReadBufferSize: dapr.DaprHttpReadBufferSize,
					LogLevel:           dapr.LogLevel,
				}

				break
			}
		}

		if err := b.buildEnvBlock(project.Env, &projectTemplateCtx); err != nil {
			return err
		}

		b.containerAppTemplateContexts[resourceName] = projectTemplateCtx
	}

	for moduleName, module := range b.bicepContext.BicepModules {
		for paramName, paramValue := range module.Params {
			// bicep uses ' instead of " for strings, so we need to replace all " with '
			singleQuoted := strings.ReplaceAll(paramValue, "\"", "'")

			var evaluationError error
			evaluatedString := singleQuotedStringRegex.ReplaceAllStringFunc(singleQuoted, func(s string) string {
				evaluatedString, err := EvalString(s, func(s string) (string, error) {
					return b.evalBindingRef(s, inputEmitTypeBicep)
				})
				if err != nil {
					evaluationError = fmt.Errorf("evaluating bicep module %s parameter %s: %w", moduleName, paramName, err)
				}
				return evaluatedString
			})
			if evaluationError != nil {
				return evaluationError
			}

			// quick check to know if evaluatedString is only holding one only reference. If so, we don't need to use
			// the form of '%{ref}' and we can directly use ref alone.
			if isComplexExp, val := isComplexExpression(evaluatedString); !isComplexExp {
				module.Params[paramName] = val
				continue
			}

			// Property names that are valid identifiers should be declared without quotation marks and accessed
			// using dot notation.
			evaluatedString = propertyNameRegex.ReplaceAllString(evaluatedString, "${1}:")

			// restore double {{ }} to single { } for bicep output
			// we used double only during the evaluation to scape single brackets
			module.Params[paramName] = strings.ReplaceAll(strings.ReplaceAll(evaluatedString, "'{{", "${"), "}}'", "}")
		}
	}

	for _, kv := range b.bicepContext.KeyVaults {
		if kv.ReadAccessPrincipalId {
			b.bicepContext.RequiresPrincipalId = true
			break
		}
	}

	return nil
}

// isComplexExpression checks if the evaluatedString is in the form of '{{ expr }}' or if it is a complex expression like
// 'foo {{ expr }} bar {{ expr2 }}' and returns true if it is a complex expression.
// When the expression is not complex, it returns false and the evaluatedString without the special characters.
func isComplexExpression(evaluatedString string) (bool, string) {
	removeSpecialChars := strings.ReplaceAll(strings.ReplaceAll(evaluatedString, "'{{", ""), "}}'", "")
	if evaluatedString == fmt.Sprintf("'{{%s}}'", removeSpecialChars) {
		return false, removeSpecialChars
	}
	return true, ""
}

// buildIngress builds the ingress configuration for a given set of bindings. It returns nil, nil if no ingress should
// be configured (i.e. the bindings are empty).
func buildIngress(bindings map[string]*Binding) (*genContainerAppIngress, error) {
	binding, err := validateAndMergeBindings(bindings)
	if err != nil {
		return nil, err
	}

	if binding != nil {
		if binding.ContainerPort == nil {
			return nil, fmt.Errorf(
				"binding for does not specify a container port, " +
					"ensure WithServiceBinding for this resource specifies a hostPort value")
		}

		return &genContainerAppIngress{
			External:      binding.External,
			Transport:     binding.Transport,
			TargetPort:    *binding.ContainerPort,
			AllowInsecure: strings.ToLower(binding.Transport) == "http2" || !binding.External,
		}, nil
	}

	return nil, nil
}

// inputEmitType controls how references to inputs are emitted in the generated file.
type inputEmitType string

const inputEmitTypeBicep inputEmitType = "bicep"
const inputEmitTypeYaml inputEmitType = "yaml"

// evalBindingRef evaluates a binding reference expression based on the state of the manifest loaded into the generator.
func (b infraGenerator) evalBindingRef(v string, emitType inputEmitType) (string, error) {
	parts := strings.SplitN(v, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("malformed binding expression, expected <resourceName>.<propertyPath> but was: %s", v)
	}

	resource, prop := parts[0], parts[1]
	targetType, ok := b.resourceTypes[resource]
	if !ok {
		return "", fmt.Errorf("unknown resource referenced in binding expression: %s", resource)
	}

	if connectionString, has := b.connectionStrings[resource]; has && prop == "connectionString" {
		// The connection string can be a expression itself, so we need to evaluate it.
		res, err := EvalString(connectionString, func(s string) (string, error) {
			return b.evalBindingRef(s, emitType)
		})
		if err != nil {
			return "", fmt.Errorf("evaluating connection string for %s: %w", resource, err)
		}

		return res, nil
	}
	if valueString, has := b.valueStrings[resource]; has && prop == "value" {
		// The value string can be a expression itself, so we need to evaluate it.
		res, err := EvalString(valueString, func(s string) (string, error) {
			return b.evalBindingRef(s, emitType)
		})
		if err != nil {
			return "", fmt.Errorf("evaluating value.v0's value string for %s: %w", resource, err)
		}

		return res, nil
	}

	if strings.HasPrefix(prop, "inputs.") {
		parts := strings.Split(prop[len("inputs."):], ".")

		if len(parts) != 1 {
			return "", fmt.Errorf("malformed binding expression, expected inputs.<input-name> but was: %s", v)
		}

		switch emitType {
		case inputEmitTypeBicep:
			return fmt.Sprintf("${inputs['%s']['%s']}", resource, parts[0]), nil
		case inputEmitTypeYaml:
			return fmt.Sprintf("{{ index .Inputs `%s` `%s` }}", resource, parts[0]), nil
		default:
			panic(fmt.Sprintf("unexpected inputEmitType %s", string(emitType)))
		}
	}

	switch {
	case targetType == "project.v0" || targetType == "container.v0" || targetType == "dockerfile.v0":
		if !strings.HasPrefix(prop, "bindings.") {
			return "", fmt.Errorf("unsupported property referenced in binding expression: %s for %s", prop, targetType)
		}

		parts := strings.Split(prop[len("bindings."):], ".")

		if len(parts) != 2 {
			return "", fmt.Errorf("malformed binding expression, expected "+
				"bindings.<binding-name>.<property> but was: %s", v)
		}

		var binding *Binding
		var has bool

		if targetType == "project.v0" {
			binding, has = b.projects[resource].Bindings[parts[0]]
		} else if targetType == "container.v0" {
			binding, has = b.containers[resource].Bindings[parts[0]]
		} else if targetType == "dockerfile.v0" {
			binding, has = b.dockerfiles[resource].Bindings[parts[0]]
		}

		if !has {
			return "", fmt.Errorf("unknown binding referenced in binding expression: %s for resource %s", parts[0], resource)
		}

		switch parts[1] {
		case "host":
			// The host name matches the containerapp name, so we can just return the resource name.
			return resource, nil
		case "port":
			return fmt.Sprintf(`%d`, *binding.ContainerPort), nil
		case "url":
			var urlFormatString string

			if binding.External {
				urlFormatString = "%s://%s.{{ .Env.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN }}"
			} else {
				urlFormatString = "%s://%s.internal.{{ .Env.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN }}"
			}

			return fmt.Sprintf(urlFormatString, binding.Scheme, resource), nil
		default:
			return "",
				fmt.Errorf("malformed binding expression, expected bindings.<binding-name>.[host|port|url] but was: %s", v)
		}
	case targetType == "postgres.database.v0" ||
		targetType == "redis.v0" ||
		targetType == "azure.cosmosdb.account.v0" ||
		targetType == "azure.cosmosdb.database.v0" ||
		targetType == "azure.sql.v0" ||
		targetType == "azure.sql.database.v0" ||
		targetType == "sqlserver.server.v0" ||
		targetType == "sqlserver.database.v0":
		switch prop {
		case "connectionString":
			// returns something like {{ connectionString "resource" }}
			return fmt.Sprintf(`{{ connectionString "%s" }}`, resource), nil
		default:
			return "", errUnsupportedProperty(targetType, prop)
		}
	case targetType == "azure.servicebus.v0":
		switch prop {
		case "connectionString":
			return fmt.Sprintf("{{ urlHost .Env.SERVICE_BINDING_%s_ENDPOINT }}", scaffold.AlphaSnakeUpper(resource)), nil
		default:
			return "", errUnsupportedProperty("azure.servicebus.v0", prop)
		}
	case targetType == "azure.appinsights.v0":
		switch prop {
		case "connectionString":
			return fmt.Sprintf("{{ .Env.SERVICE_BINDING_%s_CONNECTION_STRING }}", scaffold.AlphaSnakeUpper(resource)), nil
		default:
			return "", errUnsupportedProperty("azure.appinsights.v0", prop)
		}
	case targetType == "azure.keyvault.v0" ||
		targetType == "azure.storage.blob.v0" ||
		targetType == "azure.storage.queue.v0" ||
		targetType == "azure.storage.table.v0":
		switch prop {
		case "connectionString":
			return fmt.Sprintf("{{ .Env.SERVICE_BINDING_%s_ENDPOINT }}", scaffold.AlphaSnakeUpper(resource)), nil
		default:
			return "", errUnsupportedProperty(targetType, prop)
		}
	case targetType == "azure.bicep.v0":
		if !strings.HasPrefix(prop, "outputs.") && !strings.HasPrefix(prop, "secretOutputs.") {
			return "", fmt.Errorf("unsupported property referenced in binding expression: %s for %s", prop, targetType)
		}
		replaceDash := strings.ReplaceAll(resource, "-", "_")
		outputParts := strings.SplitN(prop, ".", 2)
		outputType := outputParts[0]
		outputName := outputParts[1]
		if outputType == "outputs" {
			if emitType == inputEmitTypeYaml {
				return fmt.Sprintf("{{ .Env.%s_%s }}", strings.ToUpper(replaceDash), strings.ToUpper(outputName)), nil
			}
			if emitType == inputEmitTypeBicep {
				// using `{{ }}` helps to check if the result of evaluating a string is a complex expression or not.
				return fmt.Sprintf("{{%s.outputs.%s}}", replaceDash, outputName), nil
			}
			return "", fmt.Errorf("unexpected output type %s", string(emitType))
		} else {
			if emitType == inputEmitTypeYaml {
				return fmt.Sprintf(
					"{{ secretOutput {{ .Env.SERVICE_BINDING_%s_ENDPOINT }}secrets/%s }}",
					strings.ToUpper(replaceDash+"kv"),
					outputName), nil
			}
			if emitType == inputEmitTypeBicep {
				return "", fmt.Errorf("secretOutputs not supported as inputs for bicep modules")
			}
			return "", fmt.Errorf("unexpected output type %s", string(emitType))
		}
	case targetType == "parameter.v0":
		param := b.bicepContext.InputParameters[resource]
		inputType := "parameter"
		if param.Secret {
			inputType = "securedParameter"
		}
		replaceDash := strings.ReplaceAll(resource, "-", "_")
		switch emitType {
		case inputEmitTypeBicep:
			return fmt.Sprintf("{{%s}}", replaceDash), nil
		case inputEmitTypeYaml:
			return fmt.Sprintf(`{{ %s "%s" }}`, inputType, replaceDash), nil
		default:
			panic(fmt.Sprintf("unexpected parameter %s", string(emitType)))
		}
	default:
		ignore, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES"))
		if err == nil && ignore {
			log.Printf("ignoring binding reference to resource of type %s since "+
				"AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES is set", targetType)

			return fmt.Sprintf("!!! expression '%s' to type '%s' unsupported by azd !!!", v, targetType), nil
		}

		return "", fmt.Errorf("unsupported resource type %s referenced in binding expression", targetType)
	}
}

// buildEnvBlock creates the environment map in the template context. It does this by copying the values from the given map,
// evaluating any binding expressions that are present. It writes the result of the evaluation after calling json.Marshal
// so the values may be emitted into YAML as is without worrying about escaping.
func (b *infraGenerator) buildEnvBlock(env map[string]string, manifestCtx *genContainerAppManifestTemplateContext) error {
	for k, value := range env {
		res, err := EvalString(value, func(s string) (string, error) { return b.evalBindingRef(s, inputEmitTypeYaml) })
		if err != nil {
			return fmt.Errorf("evaluating value for %s: %w", k, err)
		}

		// We want to ensure that we render these values in the YAML as strings.  If `res` was the string "true"
		// (without the quotes), we would naturally create a value directive in yaml that looks like this:
		//
		// - name: OTEL_DOTNET_EXPERIMENTAL_OTLP_EMIT_EXCEPTION_LOG_ATTRIBUTES
		//   value: true
		//
		// And YAML rules would treat the above as the value being a boolean instead of a string, which the container
		// app service expects.
		//
		// YAML marshalling the string value will give us something like `"true"` (with the quotes, and any escaping
		// that needs to be done), which is what we want here.
		// Do not use JSON marshall as it would escape the quotes within the string, breaking the meaning of the value.
		// yaml marshall will use 'some text "quoted" more text' as a valid yaml string.
		yamlString, err := yaml.Marshal(res)
		if err != nil {
			return fmt.Errorf("marshalling env value: %w", err)
		}

		// remove the trailing newline. yaml marshall will add a newline at the end of the string, as the new line is
		// expected at the end of the yaml document. But we are getting a single value with valid yaml here, so we don't
		// need the newline
		resolvedValue := string(yamlString[0 : len(yamlString)-1])

		// connectionString detection, either of:
		//  a) explicit connection string key for env, like "ConnectionStrings__resource": "XXXXX"
		//  b) a connection string field references in the value, like "FOO": "{resource.connectionString}"
		//  c) found placeholder for a connection string within resolved value, like "{{ connectionString resource }}"
		//  d) found placeholder for a secured-param, like "{{ securedParameter param }}"
		//  e) found placeholder for a secret output, like "{{ secretOutput kv secret }}"
		if strings.Contains(k, "ConnectionStrings__") || // a)
			strings.Contains(value, ".connectionString}") || // b)
			strings.Contains(resolvedValue, "{{ connectionString") || // c)
			strings.Contains(resolvedValue, "{{ securedParameter ") || // d)
			strings.Contains(resolvedValue, "{{ secretOutput ") { // e)

			// handle secret-outputs:
			// secret outputs can be set either as a direct reference to a key vault secret, or as secret within the
			// container apps. Below code checks if the the resolved value is a complex expression like:
			// `key:{{ secretOutput kv secret }};foo;bar`.
			// If the resolved value is not complex, it can become a direct reference to key vault secret, otherwise it
			// is set as a secret within the container app.
			if strings.Contains(resolvedValue, "{{ secretOutput ") {
				if isComplexExp, _ := isComplexExpression(resolvedValue); !isComplexExp {
					removeBrackets := strings.ReplaceAll(
						strings.ReplaceAll(resolvedValue, " }}'", "'"), "{{ secretOutput ", "")
					manifestCtx.KeyVaultSecrets[k] = removeBrackets
					continue
				}
				// complex expression using secretOutput:
				// The secretOutput is a reference to a KeyVault secret but can't be set as KeyVault secret reference
				// because the secret is just part of the full value.
				// For such case, the secret value is pulled during deployment and replaced in the containerApp.yaml file
				// as a secret within the containerApp.
				resolvedValue = secretOutputForDeployTemplate(resolvedValue)
			}
			manifestCtx.Secrets[k] = resolvedValue
			continue
		}
		manifestCtx.Env[k] = resolvedValue
	}

	return nil
}

// secretOutputRegex is a regular expression used to match and extract secret output references in a specific format.
var secretOutputRegex = regexp.MustCompile(`{{ secretOutput {{ \.Env\.(.*) }}secrets/(.*) }}`)

// secretOutputForDeployTemplate replaces all the instances like `{{ secretOutput {{ .Env.[host] }}secrets/[secretName] }}`
// with `{{ secretOutput [host] "secretName" }}`, creating a placeholder to be resolved during the deployment.
func secretOutputForDeployTemplate(secretName string) string {
	return secretOutputRegex.ReplaceAllString(secretName, `{{ secretOutput "$1" "$2" }}`)
}

// errUnsupportedProperty returns an error indicating that the given property is not supported for the given resource.
func errUnsupportedProperty(resourceType, propertyName string) error {
	return fmt.Errorf("unsupported property referenced in binding expression: %s for %s", propertyName, resourceType)
}

// executeToFS executes the given template with the given name and context, and writes the result to the given path in
// the given target filesystem.
func executeToFS(targetFS *memfs.FS, tmpl *template.Template, name string, path string, context any) error {
	buf := bytes.NewBufferString("")

	if err := tmpl.ExecuteTemplate(buf, name, context); err != nil {
		return fmt.Errorf("executing template: %w", err)
	}

	if err := targetFS.MkdirAll(filepath.Dir(path), osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	if err := targetFS.WriteFile(path, buf.Bytes(), osutil.PermissionFile); err != nil {
		return fmt.Errorf("writing file: %w", err)
	}

	return nil
}
