package apphost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/custommaps"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/braydonk/yaml"
	"github.com/psanford/memfs"
)

const RedisContainerAppService = "redis"

const DaprStateStoreComponentType = "state"
const DaprPubSubComponentType = "pubsub"

// genTemplates is the collection of templates that are used when generating infrastructure files from a manifest.
var genTemplates *template.Template

type AspireDashboard struct {
	Link string
}

func (aspireD *AspireDashboard) ToString(currentIndentation string) string {
	return fmt.Sprintf("%sAspire Dashboard: %s", currentIndentation, output.WithLinkFormat(aspireD.Link))
}

func (aspireD *AspireDashboard) MarshalJSON() ([]byte, error) {
	return json.Marshal(*aspireD)
}

func AspireDashboardUrl(
	ctx context.Context,
	env *environment.Environment,
	alphaFeatureManager *alpha.FeatureManager) *AspireDashboard {

	ContainersManagedEnvHost, exists := env.LookupEnv("AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN")
	if !exists {
		return nil
	}

	return &AspireDashboard{
		Link: fmt.Sprintf("https://aspire-dashboard.ext.%s", ContainersManagedEnvHost),
	}
}

func init() {
	tmpl, err := template.New("templates").
		Option("missingkey=error").
		Funcs(
			template.FuncMap{
				"toLower":   strings.ToLower,
				"bicepName": scaffold.BicepName,
				"mergeBicepName": func(src ...string) string {
					return scaffold.BicepName(strings.Join(src, "-"))
				},
				"alphaSnakeUpper":        scaffold.AlphaSnakeUpper,
				"containerAppName":       scaffold.ContainerAppName,
				"containerAppSecretName": scaffold.ContainerAppSecretName,
				"fixBackSlash": func(src string) string {
					return strings.ReplaceAll(src, "\\", "/")
				},
				"bicepParameterName": func(src string) string {
					return strings.ReplaceAll(src, "-", "_")
				},
				"removeDot": scaffold.RemoveDotAndDash,
				"envFormat": scaffold.EnvFormat,
				"bicepParameterValue": func(value *string) string {
					if value == nil {
						return ""
					}
					return fmt.Sprintf(" = '%s'", *value)
				},
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
		case "project.v0", "project.v1":
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
				Args:      comp.Args,
			}
		}
	}

	return res
}

// Containers returns information about all container.v0 resources from a manifest.
func Containers(manifest *Manifest) map[string]genContainer {
	res := make(map[string]genContainer)

	for name, comp := range manifest.Resources {
		switch comp.Type {
		case "container.v0":
			res[name] = genContainer{
				Image:      *comp.Image,
				Env:        comp.Env,
				Bindings:   comp.Bindings,
				Inputs:     comp.Inputs,
				Volumes:    comp.Volumes,
				BindMounts: comp.BindMounts,
				Args:       comp.Args,
			}
		}
	}

	return res
}

// BuildContainers returns information about all container.v1 resources from a manifest.
func BuildContainers(manifest *Manifest) (map[string]genBuildContainer, error) {
	res := make(map[string]genBuildContainer)

	for name, comp := range manifest.Resources {
		switch comp.Type {
		case "container.v1":
			bc, err := buildContainerFromResource(comp)
			if err != nil {
				return nil, fmt.Errorf("building container from resource %s: %w", name, err)
			}
			res[name] = *bc
		}
	}

	return res, nil
}

type AppHostOptions struct {
	AzdOperations bool
}

type ContainerAppManifestType string

const (
	ContainerAppManifestTypeYAML  ContainerAppManifestType = "yaml"
	ContainerAppManifestTypeBicep ContainerAppManifestType = "bicep"
)

func ContainerSourceBicepContent(
	manifest *Manifest, projectName string, options AppHostOptions) (string, error) {
	templateFs, err := BicepTemplate(projectName, manifest, options)
	if err != nil {
		return "", err
	}
	sourceName := filepath.Base(*manifest.Resources[projectName].Deployment.Path)
	file, err := templateFs.Open(filepath.Join(projectName, sourceName))
	if err != nil {
		return "", fmt.Errorf("opening bicep source file: %w", err)
	}
	defer file.Close()
	// read the file content into a string
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(file); err != nil {
		return "", fmt.Errorf("reading bicep source file: %w", err)
	}
	return buf.String(), nil
}

// ContainerAppManifestTemplateForProject returns the container app manifest template for a given project.
// It can be used (after evaluation) to deploy the service to a container app environment.
// When the projectName contains `Deployment` it will generate a bicepparam template instead of the yaml template.
func ContainerAppManifestTemplateForProject(
	manifest *Manifest, projectName string, options AppHostOptions) (string, ContainerAppManifestType, error) {
	generator := newInfraGenerator()

	if err := generator.LoadManifest(manifest); err != nil {
		return "", "", err
	}

	if err := generator.Compile(); err != nil {
		return "", "", err
	}

	var buf bytes.Buffer

	type yamlTemplateCtx struct {
		genContainerAppManifestTemplateContext
		TargetPortExpression string
	}
	tCtx := generator.containerAppTemplateContexts[projectName]
	tmplCtx := yamlTemplateCtx{
		genContainerAppManifestTemplateContext: tCtx,
	}

	if tCtx.Ingress != nil {
		if tCtx.Ingress.TargetPort != 0 && !tCtx.Ingress.UsingDefaultPort {
			// not using default port makes this to be a non-changing value
			tmplCtx.TargetPortExpression = fmt.Sprintf("%d", tCtx.Ingress.TargetPort)
		} else {
			tmplCtx.TargetPortExpression = fmt.Sprintf("{{ targetPortOrDefault %d }}", tCtx.Ingress.TargetPort)
		}
	}

	// replace the containerPort with the targetPort expression
	for p, v := range tmplCtx.DeployParams {
		if v == "'{{ containerPort }}'" {
			tmplCtx.DeployParams[p] = fmt.Sprintf("'%s'", tmplCtx.TargetPortExpression)
		}
	}

	var manifestType ContainerAppManifestType
	if len(tCtx.DeployParams) == 0 {
		manifestType = ContainerAppManifestTypeYAML
		err := genTemplates.ExecuteTemplate(&buf, "containerApp.tmpl.yaml", tmplCtx)
		if err != nil {
			return "", "", fmt.Errorf("executing template: %w", err)
		}
	} else {
		manifestType = ContainerAppManifestTypeBicep
		err := genTemplates.ExecuteTemplate(&buf, "containerApp.tmpl.bicepparam", tmplCtx)
		if err != nil {
			return "", "", fmt.Errorf("executing bicepparam template: %w", err)
		}
	}

	return buf.String(), manifestType, nil
}

// BicepTemplate returns a filesystem containing the generated bicep files for the given manifest. These files represent
// the shared infrastructure that would normally be under the `infra/` folder for the given manifest.
func BicepTemplate(name string, manifest *Manifest, options AppHostOptions) (*memfs.FS, error) {
	generator := newInfraGenerator()

	if err := generator.LoadManifest(manifest); err != nil {
		return nil, err
	}

	if err := generator.Compile(); err != nil {
		return nil, err
	}

	// Aspire Dashboard workaround
	// By setting this, we will give Contributor role to the user running azd for the Container Apps Environment
	// See: https://github.com/Azure/azure-dev/issues/3928
	generator.bicepContext.RequiresPrincipalId = true

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
		Value  *string
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
	genParametersKeys := slices.Sorted(maps.Keys(generator.bicepContext.InputParameters))
	metadataType := azure.AzdMetadataTypeGenerate
	for _, key := range genParametersKeys {
		parameter := generator.bicepContext.InputParameters[key]
		parameterMetadata := ""
		var parameterDefaultValue *string
		if parameter.Default != nil {
			// main.bicep template handles *string for default.Value. If the value is nil, it will be ignored.
			// if not nil, like empty string or any other string, it is used as `= '<value>'`
			if parameter.Default.Value != nil {
				parameterDefaultValue = parameter.Default.Value
				metadataType = azure.AzdMetadataTypeNeedForDeploy
				parameterMetadata = "{}"
			} else if parameter.Default.Generate != nil { // Note: .Value and .Generate are mutually exclusive
				pMetadata, err := inputMetadata(*parameter.Default.Generate)
				if err != nil {
					return nil, fmt.Errorf("generating input metadata for %s: %w", key, err)
				}
				parameterMetadata = pMetadata
			}
			// Note: azd is not checking or validating that Default.Generate and Default.Value are not both set.
			// The AppHost prevents this from happening by not allowing both to be set at the same time.
		}
		input := genInput{Name: key, Secret: parameter.Secret, Type: parameter.Type, Value: parameterDefaultValue}
		parameters = append(parameters, autoGenInput{
			genInput:       input,
			MetadataConfig: parameterMetadata,
			MetadataType:   metadataType})
		if slices.Contains(generator.bicepContext.mappedParameters, strings.ReplaceAll(key, "-", "_")) {
			mapToResourceParams = append(mapToResourceParams, input)
		}
	}
	context := bicepContext{
		genBicepTemplateContext: generator.bicepContext,
		WithMetadataParameters:  parameters,
		MainToResourcesParams:   mapToResourceParams,
	}
	if err := executeToFS(fs, genTemplates, "main.bicep", name+".bicep", context); err != nil {
		return nil, fmt.Errorf("generating infra/main.bicep: %w", err)
	}

	if err := executeToFS(fs, genTemplates, "resources.bicep", "resources.bicep", context); err != nil {
		return nil, fmt.Errorf("generating infra/resources.bicep: %w", err)
	}

	if err := executeToFS(
		fs, genTemplates, "main.parameters.json", name+".parameters.json", generator.bicepContext); err != nil {
		return nil, fmt.Errorf("generating infra/resources.bicep: %w", err)
	}

	// azd operations
	if generator.bicepContext.HasBindMounts {
		if options.AzdOperations {
			if err := executeToFS(
				fs, genTemplates, "azd.operations.yaml", "azd.operations.yaml", generator.bicepContext); err != nil {
				return nil, fmt.Errorf("generating infra/azd.operations.yaml: %w", err)
			}
		} else {
			// returning fs because this error can be handled by the caller as expected
			return fs, provisioning.ErrBindMountOperationDisabled
		}

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

	generatedFS := memfs.New()

	projectFileContext := genProjectFileContext{
		Name: projectName,
		Services: map[string]string{
			"app": fmt.Sprintf("./%s", filepath.ToSlash(appHostRel)),
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
	dapr              map[string]genDapr
	projects          map[string]genProject
	connectionStrings map[string]string
	// keeps the value from value.v0 resources if provided.
	valueStrings  map[string]string
	resourceTypes map[string]string

	bicepContext                 genBicepTemplateContext
	containerAppTemplateContexts map[string]genContainerAppManifestTemplateContext
	allServicesIngress           map[string]ingressDetails
	// works for container.v0, container.v1 and dockerfile.v0
	buildContainers map[string]genBuildContainer
}

func newInfraGenerator() *infraGenerator {
	return &infraGenerator{
		bicepContext: genBicepTemplateContext{
			ContainerAppEnvironmentServices: make(map[string]genContainerAppEnvironmentServices),
			KeyVaults:                       make(map[string]genKeyVault),
			ContainerApps:                   make(map[string]genContainerApp),
			DaprComponents:                  make(map[string]genDaprComponent),
			InputParameters:                 make(map[string]Input),
			BicepModules:                    make(map[string]genBicepModules),
			OutputParameters:                make(map[string]genOutputParameter),
			OutputSecretParameters:          make(map[string]genOutputParameter),
		},
		dapr:                         make(map[string]genDapr),
		projects:                     make(map[string]genProject),
		connectionStrings:            make(map[string]string),
		resourceTypes:                make(map[string]string),
		containerAppTemplateContexts: make(map[string]genContainerAppManifestTemplateContext),
		buildContainers:              make(map[string]genBuildContainer),
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

	feedFrom := func(from map[string]string, to *genBicepTemplateContext) error {
		for _, value := range from {
			outputs, err := evaluateForOutputs(value)
			if err != nil {
				return err
			}
			for key, output := range outputs {
				if strings.Contains(output.Value, ".outputs.") {
					to.OutputParameters[key] = output
				} else {
					to.OutputSecretParameters[key] = output
				}
			}

		}
		return nil
	}
	err := feedFrom(resource.Env, &b.bicepContext)
	if err != nil {
		return err
	}

	if resource.Deployment != nil {
		// Taking only the string values from the deployment parameters. There could be other types like int or object there.
		// Only string type could be referencing outputs.
		deploymentParams := map[string]string{}
		for k, v := range resource.Deployment.Params {
			stringValue, castOk := v.(string)
			if !castOk {
				continue
			}
			deploymentParams[k] = stringValue
		}

		err = feedFrom(deploymentParams, &b.bicepContext)
		if err != nil {
			return err
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
		case "project.v0":
			b.addProject(name, *comp.Path, comp.Env, comp.Bindings, comp.Args, nil, "")
		case "project.v1":
			var deploymentParams map[string]any
			var deploymentSource string
			if comp.Deployment != nil {
				deploymentParams = comp.Deployment.Params
				deploymentSource = filepath.Base(*comp.Deployment.Path)
			}
			b.addProject(name, *comp.Path, comp.Env, comp.Bindings, comp.Args, deploymentParams, deploymentSource)
		case "container.v0":
			err := b.addBuildContainer(name, comp)
			if err != nil {
				return err
			}
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
		case "container.v1":
			err := b.addBuildContainer(name, comp)
			if err != nil {
				return err
			}
		case "dockerfile.v0":
			err := b.addBuildContainer(name, comp)
			if err != nil {
				return err
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

func (b *infraGenerator) hasBindMounts() {
	b.bicepContext.HasBindMounts = true
}

func (b *infraGenerator) addInputParameter(name string, comp *Resource) error {
	input, err := InputParameter(name, comp)
	if err != nil {
		return fmt.Errorf("resolving input for parameter %s: %w", name, err)
	}

	if input == nil {
		// no inputs in the value, nothing to do
		return nil
	}

	b.bicepContext.InputParameters[name] = *input
	return nil
}

// InputParameter gets the Input from a parameter. If the parameter does not have an input, it returns nil.
func InputParameter(name string, comp *Resource) (*Input, error) {
	pValue := comp.Value

	if !hasInputs(pValue) {
		// no inputs in the value, nothing to do
		return nil, nil
	}

	input, err := resolveResourceInput(name, comp)
	if err != nil {
		return nil, fmt.Errorf("resolving input for parameter %s: %w", name, err)
	}
	return &input, nil
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
		b.addKeyVault("kv"+uniqueFnvNumber(name), true, true)
	}
	if _, hasLocation := stringParams["location"]; !hasLocation {
		// if location is not provided, add it as a link to location parameter
		stringParams["location"] = "location"
	}

	b.bicepContext.BicepModules[name] = genBicepModules{Path: *comp.Path, Params: stringParams}
	return nil
}

const (
	knownParameterKeyVault         string = "keyVaultName"
	knownParameterPrincipalId      string = "principalId"
	knownParameterPrincipalType    string = "principalType"
	knownParameterPrincipalName    string = "principalName"
	knownParameterLogAnalytics     string = "logAnalyticsWorkspaceId"
	knownParameterContainerEnvName string = "containerAppEnvironmentName"
	knownParameterContainerEnvId   string = "containerAppEnvironmentId"

	knownInjectedValuePrincipalId      string = "resources.outputs.MANAGED_IDENTITY_PRINCIPAL_ID"
	knownInjectedValuePrincipalType    string = "'ServicePrincipal'"
	knownInjectedValuePrincipalName    string = "resources.outputs.MANAGED_IDENTITY_NAME"
	knownInjectedValueLogAnalytics     string = "resources.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_ID"
	knownInjectedValueContainerEnvName string = "resources.outputs.AZURE_CONTAINER_APPS_ENVIRONMENT_NAME"
	knownInjectedValueContainerEnvId   string = "resources.outputs.AZURE_CONTAINER_APPS_ENVIRONMENT_ID"
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
		uniqueName := "kv" + uniqueFnvNumber(resourceName)
		return fmt.Sprintf("resources.outputs.SERVICE_BINDING_%s_NAME", strings.ToUpper(uniqueName)), true, nil
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
	if p == knownParameterContainerEnvName {
		return knownInjectedValueContainerEnvName, true, nil
	}
	if p == knownParameterContainerEnvId {
		return knownInjectedValueContainerEnvId, true, nil
	}
	return finalParamValue, false, nil
}

// uniqueFnvNumber generates a unique FNV hash number for the given string value.
// It uses the FNV-1a hash algorithm to calculate a 32-bit hash value.
// The generated 32-bit hash number is returned as an 8-length hexadecimal string.
func uniqueFnvNumber(val string) string {
	hash := fnv.New32a()
	hash.Write([]byte(val))
	return fmt.Sprintf("%x", hash.Sum32())
}

func (b *infraGenerator) addProject(
	name string,
	path string,
	env map[string]string,
	bindings custommaps.WithOrder[Binding],
	args []string,
	deploymentParams map[string]any,
	deploymentSource string,
) {
	b.requireCluster()
	b.requireContainerRegistry()

	b.projects[name] = genProject{
		Path:             path,
		Env:              env,
		Bindings:         bindings,
		Args:             args,
		DeploymentParams: deploymentParams,
		DeploymentSource: deploymentSource,
	}
}

func (b *infraGenerator) addContainerAppService(name string, serviceType string) {
	b.requireCluster()

	b.bicepContext.ContainerAppEnvironmentServices[name] = genContainerAppEnvironmentServices{
		Type: serviceType,
	}
}

func (b *infraGenerator) addKeyVault(name string, noTags, readAccessPrincipalId bool) {
	b.bicepContext.KeyVaults[name] = genKeyVault{
		NoTags:                noTags,
		ReadAccessPrincipalId: readAccessPrincipalId,
	}
}

// buildContainer represents a container defined with a pre-build image or a build context.
// container.v0 resources are used to define containers with pre-built images.
//   - uses image field
//
// dockerfile.v0 resources are used to define containers with build context.
//   - uses path and context fields
//
// container.v1 resources are used to define containers with either build context or pre-built images.
//   - uses image field or build field
func (b *infraGenerator) addBuildContainer(
	name string,
	r *Resource) error {
	if r.Image != nil && r.Build != nil {
		return fmt.Errorf("Resource '%s' cannot have both an image and a build", name)
	}

	b.requireCluster()
	if len(r.Volumes) > 0 {
		b.requireStorageVolume()
	}

	if len(r.BindMounts) > 0 {
		b.requireStorageVolume()
		b.hasBindMounts()
	}

	bc, err := buildContainerFromResource(r)
	if err != nil {
		return fmt.Errorf("container resource '%s': %w", name, err)
	}
	if bc.Build != nil {
		b.requireContainerRegistry()
	}
	b.buildContainers[name] = *bc
	return nil
}

func buildContainerFromResource(r *Resource) (*genBuildContainer, error) {
	// common fields for all build containers
	var deploymentParams map[string]any
	var deploymentSource string
	defaultTargetPort := 80
	// container.v1 uses default target port 8080
	if r.Type == "container.v1" {
		defaultTargetPort = 8080
	}
	if r.Deployment != nil {
		deploymentParams = r.Deployment.Params
		deploymentSource = filepath.Base(*r.Deployment.Path)
	}
	bc := &genBuildContainer{
		Entrypoint:        r.Entrypoint,
		Args:              r.Args,
		Env:               r.Env,
		Bindings:          r.Bindings,
		Volumes:           r.Volumes,
		DeploymentParams:  deploymentParams,
		DeploymentSource:  deploymentSource,
		BindMounts:        r.BindMounts,
		DefaultTargetPort: defaultTargetPort,
	}

	// container.v0 and container.v1+pre-build image
	if r.Image != nil {
		bc.Image = *r.Image
		return bc, nil
	}

	// details to build container, either from dockerfile.v0 or container.v1
	var build *genBuildContainerDetails

	// dockerfile.v0
	if r.Context != nil {
		build = &genBuildContainerDetails{
			Context: *r.Context,
			Args:    nil, // dockerfile.v0 does not support build args, it only has top level args []string
		}
		if r.Path != nil {
			build.Dockerfile = *r.Path
		}
	} else

	// container.v1+build
	if r.Build != nil {
		build = &genBuildContainerDetails{
			Context:    r.Build.Context,
			Dockerfile: r.Build.Dockerfile,
			Args:       r.Build.Args,
			Secrets:    r.Build.Secrets,
		}
	}

	if build == nil {
		return nil, fmt.Errorf("container resource must have either an image, context or a build")
	}

	bc.Build = build
	return bc, nil
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

// singleQuotedStringRegex is a regular expression pattern used to match single-quoted strings.
var singleQuotedStringRegex = regexp.MustCompile(`'[^']*'`)
var propertyNameRegex = regexp.MustCompile(`'([^']*)':`)
var jsonSimpleKeyRegex = regexp.MustCompile(`"([a-zA-Z0-9]*)":`)

type ingressDetails struct {
	// aca ingress definition
	ingress *genContainerAppIngress
	// list of bindings from the service which are bind to the the ingress
	ingressBindings []string
}

func (b *infraGenerator) compileIngress() error {
	result := make(map[string]ingressDetails)
	for name, project := range b.projects {
		ingress, bindingsFromIngress, err := buildAcaIngress(project.Bindings, 8080)
		if err != nil {
			return fmt.Errorf("configuring ingress for resource %s: %w", name, err)
		}
		result[name] = ingressDetails{
			ingress:         ingress,
			ingressBindings: bindingsFromIngress,
		}
	}
	for name, bc := range b.buildContainers {
		ingress, bindingsFromIngress, err := buildAcaIngress(bc.Bindings, bc.DefaultTargetPort)
		if err != nil {
			return fmt.Errorf("configuring ingress for resource %s: %w", name, err)
		}
		result[name] = ingressDetails{
			ingress:         ingress,
			ingressBindings: bindingsFromIngress,
		}
	}
	b.allServicesIngress = result
	return nil
}

// Compile compiles the loaded manifest into the internal representation used to generate the infrastructure files. Once
// called the context objects on the infraGenerator can be passed to the text templates to generate the required
// infrastructure.
func (b *infraGenerator) Compile() error {
	// compile the ingress for all services
	// All services's ingress must be compiled before resolving the environment variables below.
	if err := b.compileIngress(); err != nil {
		return err
	}

	for resourceName, bc := range b.buildContainers {
		var bMounts []*BindMount
		if len(bc.BindMounts) > 0 {
			// must grant write role to the Storage File Share to upload data
			b.bicepContext.RequiresPrincipalId = true
		}
		for count, bm := range bc.BindMounts {
			bMounts = append(bMounts, &BindMount{
				// adding a name using the index. This name is used for naming the resource in bicep.
				Name: fmt.Sprintf("bm%d", count),
				// mount bind is not supported across devices, as it depends on a local path which might be missing in
				// another device.
				Source:   bm.Source,
				Target:   bm.Target,
				ReadOnly: bm.ReadOnly,
			})
		}

		cs := genContainerApp{
			Volumes:    bc.Volumes,
			BindMounts: bMounts,
		}

		b.bicepContext.ContainerApps[resourceName] = cs

		projectTemplateCtx := genContainerAppManifestTemplateContext{
			Name:            resourceName,
			Env:             make(map[string]string),
			Secrets:         make(map[string]string),
			KeyVaultSecrets: make(map[string]string),
			DeployParams:    make(map[string]string),
			Ingress:         b.allServicesIngress[resourceName].ingress,
			Volumes:         bc.Volumes,
			DeploySource:    bc.DeploymentSource,
			BindMounts:      bMounts,
		}

		if err := b.buildEnvBlock(bc.Env, &projectTemplateCtx); err != nil {
			return fmt.Errorf("configuring environment for resource %s: %w", resourceName, err)
		}

		if err := b.buildArgsBlock(bc.Args, &projectTemplateCtx); err != nil {
			return err
		}

		if err := b.buildDeployBlock(bc.DeploymentParams, &projectTemplateCtx); err != nil {
			return err
		}

		b.containerAppTemplateContexts[resourceName] = projectTemplateCtx
	}

	for resourceName, project := range b.projects {
		projectTemplateCtx := genContainerAppManifestTemplateContext{
			Name:            resourceName,
			Env:             make(map[string]string),
			Secrets:         make(map[string]string),
			KeyVaultSecrets: make(map[string]string),
			DeployParams:    make(map[string]string),
			Ingress:         b.allServicesIngress[resourceName].ingress,
			DeploySource:    project.DeploymentSource,
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

		if err := b.buildArgsBlock(project.Args, &projectTemplateCtx); err != nil {
			return err
		}

		if err := b.buildDeployBlock(project.DeploymentParams, &projectTemplateCtx); err != nil {
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

	if resource == "" {
		// empty resource name means is used for global properties like outputs (currently only outputs is supported)
		if !strings.HasPrefix(prop, "outputs.") {
			return "", fmt.Errorf("unsupported global property referenced in binding expression: %s", prop)
		}
		output := prop[len("outputs."):]
		return fmt.Sprintf(`{{ .Env.%s }}`, output), nil
	}

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
	case targetType == "project.v0" ||
		targetType == "container.v0" ||
		targetType == "container.v1" ||
		targetType == "dockerfile.v0" ||
		targetType == "project.v1":
		if strings.HasPrefix(prop, "containerImage") {
			return `{{ .Image }}`, nil
		}
		if strings.HasPrefix(prop, "containerPort") {
			return `{{ containerPort }}`, nil
		}
		if strings.HasPrefix(prop, "bindMounts.") {
			parts := strings.Split(prop[len("bindMounts."):], ".")
			if len(parts) != 2 {
				return "", fmt.Errorf("malformed binding expression, expected "+
					"bindMounts.<index>.<property> but was: %s", v)
			}
			index, property := parts[0], parts[1]
			if property == "storage" {
				return fmt.Sprintf(
						`{{ .Env.SERVICE_%s_VOLUME_%s_NAME }}`,
						scaffold.AlphaSnakeUpper(scaffold.RemoveDotAndDash(resource)),
						fmt.Sprintf("BM%s", index)),
					nil
			}
			return "", fmt.Errorf("unsupported property referenced in binding expression: %s for %s", prop, targetType)
		}
		if strings.HasPrefix(prop, "volumes.") {
			parts := strings.Split(prop[len("volumes."):], ".")
			if len(parts) != 2 {
				return "", fmt.Errorf("malformed binding expression, expected "+
					"volumes.<index>.<property> but was: %s", v)
			}
			index, property := parts[0], parts[1]
			if property == "storage" {
				// find the name of the volume
				// convert index string to integer
				indexInt, err := strconv.Atoi(index)
				if err != nil {
					return "", fmt.Errorf("malformed binding expression, expected "+
						"volumes.<index>.<property> but was: %s", v)
				}
				volName := b.buildContainers[resource].Volumes[indexInt].Name
				return fmt.Sprintf(
						`{{ .Env.SERVICE_%s_VOLUME_%s_NAME }}`,
						scaffold.AlphaSnakeUpper(resource),
						scaffold.AlphaSnakeUpper(scaffold.RemoveDotAndDash(volName))),
					nil
			}
			return "", fmt.Errorf("unsupported property referenced in binding expression: %s for %s", prop, targetType)
		}
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
		bindingName := parts[0]
		bindingProperty := parts[1]

		if targetType == "project.v0" || targetType == "project.v1" {
			bindings := b.projects[resource].Bindings
			binding, has = bindings.Get(bindingName)
		} else if targetType == "container.v0" || targetType == "container.v1" || targetType == "dockerfile.v0" {
			bindings := b.buildContainers[resource].Bindings
			binding, has = bindings.Get(bindingName)
		}

		if !has {
			return "", fmt.Errorf("unknown binding referenced in binding expression: %s for resource %s", parts[0], resource)
		}
		bindingDetails, exists := b.allServicesIngress[resource]
		if !exists {
			return "", fmt.Errorf("binding reference to resource %s without ingress", resource)
		}
		var bindingMappedToMainIngress bool
		if slices.Contains(bindingDetails.ingressBindings, bindingName) {
			bindingMappedToMainIngress = true
		}

		hostNameSuffix := func(external bool) string {
			var suffix string
			switch emitType {
			case inputEmitTypeYaml:
				suffix = "{{ .Env.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN }}"
			case inputEmitTypeBicep:
				suffix = "${resources.outputs.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN}"
			default:
				panic(fmt.Sprintf("unexpected inputEmitType %s", string(emitType)))
			}

			if !external {
				suffix = "internal." + suffix
			}

			return suffix
		}

		switch bindingProperty {
		case "scheme":
			return binding.Scheme, nil
		case "protocol":
			return binding.Protocol, nil
		case "transport":
			return binding.Scheme, nil
		case "external":
			return fmt.Sprintf("%t", binding.External), nil
		case "host":
			// If the binding is mapped to the main ingress (internal or external) and it is http/https, resolution
			// expects full domain name, like `resource.internal.FQDN` or `resource.FQDN`.
			if bindingMappedToMainIngress &&
				(binding.Scheme == acaIngressSchemaHttp || binding.Scheme == acaIngressSchemaHttps) {
				return fmt.Sprintf("%s.%s", resource, hostNameSuffix(binding.External)), nil
			}
			return resource, nil
		case "targetPort":
			if binding.TargetPort != nil {
				return fmt.Sprintf("%d", *binding.TargetPort), nil
			}
			return acaTemplatedTargetPort, nil
		case "port":
			return bindingPort(binding, bindingMappedToMainIngress)
		case "url":
			var urlFormatString string

			if bindingMappedToMainIngress {
				urlFormatString = "%s://%s." + hostNameSuffix(binding.External) + "%s"
			} else {
				urlFormatString = "%s://%s%s"
			}
			var port string
			resolvedPort, err := urlPort(binding, bindingMappedToMainIngress)
			if err != nil {
				return "", err
			}
			if resolvedPort != "" {
				port = fmt.Sprintf(":%s", resolvedPort)
			}

			return fmt.Sprintf(urlFormatString, binding.Scheme, resource, port), nil
		default:
			return "",
				fmt.Errorf("malformed binding expression, expected "+
					"bindings.<binding-name>.[scheme|protocol|transport|external|host|targetPort|port|url] but was: %s", v)
		}
	case targetType == "azure.bicep.v0":
		if !strings.HasPrefix(prop, "outputs.") && !strings.HasPrefix(prop, "secretOutputs") {
			return "", fmt.Errorf("unsupported property referenced in binding expression: %s for %s", prop, targetType)
		}
		replaceDash := strings.ReplaceAll(resource, "-", "_")
		outputParts := strings.SplitN(prop, ".", 2)
		var outputType string
		var outputName string
		noOutputName := len(outputParts) == 1
		if noOutputName {
			outputType = outputParts[0]
		} else {
			outputType = outputParts[0]
			outputName = outputParts[1]
		}
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
				if noOutputName {
					return fmt.Sprintf(
						"{{ .Env.SERVICE_BINDING_%s_NAME }}",
						strings.ToUpper("kv"+uniqueFnvNumber(resource))), nil
				}
				return fmt.Sprintf(
					"{{ secretOutput {{ .Env.SERVICE_BINDING_%s_ENDPOINT }}secrets/%s }}",
					strings.ToUpper("kv"+uniqueFnvNumber(resource)),
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
			if param.Default != nil && param.Default.Value != nil {
				if param.Secret {
					return "", fmt.Errorf("default value for secured parameter %s is not supported", resource)
				}
				inputType = "parameterWithDefault"
				// parameter with default value will either use the default value or the value passed in the environment
				return fmt.Sprintf(`{{ %s "%s" "%s"}}`, inputType, replaceDash, *param.Default.Value), nil
			}
			// parameter without default value
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

// urlPort returns the port to be used when resolving a binding.
// The port for the url is not always the same as the port for the binding It depends on the Ingress configuration.
// If the binding is mapped to the ingress and it is http, the port is not used in the URL, as it would be the default
// (80 or 443).
// When not mapped to main ingress, but as an additional port, if the binding has a port defined, it is used. Otherwise
// the port is calculated from the target port.
func urlPort(binding *Binding, bindingMappedToMainIngress bool) (string, error) {
	if bindingMappedToMainIngress && (binding.Scheme == acaIngressSchemaHttp || binding.Scheme == acaIngressSchemaHttps) {
		// main ingress with Http doesn't use a port in url
		return "", nil
	}
	if binding.Port != nil {
		return fmt.Sprintf("%d", *binding.Port), nil
	}
	// additionalPorts not defining a `port` means they use the target port as the port and target port
	return urlPortFromTargetPort(binding, bindingMappedToMainIngress)
}

func bindingPort(binding *Binding, bindingMappedToMainIngress bool) (string, error) {
	if bindingMappedToMainIngress && (binding.Scheme == acaIngressSchemaHttp || binding.Scheme == acaIngressSchemaHttps) {
		if binding.Scheme == acaIngressSchemaHttp {
			return acaDefaultHttpPort, nil
		}
		if binding.Scheme == acaIngressSchemaHttps {
			return acaDefaultHttpsPort, nil
		}
	}
	if binding.Port != nil {
		return fmt.Sprintf("%d", *binding.Port), nil
	}
	if binding.TargetPort != nil {
		// Case: non-http binding w/o a port defined, but with a target port defined. (dockerfile.v0, container.v0)
		// with non-external ingress is an example here.
		return fmt.Sprintf("%d", *binding.TargetPort), nil
	}
	// no port or target port. This is the case for project.v0 where azd would get the port.
	return acaTemplatedTargetPort, nil
}

// urlPortFromTargetPort returns the port to be used when resolving a binding from the target port.
func urlPortFromTargetPort(binding *Binding, bindingMappedToMainIngress bool) (string, error) {
	if bindingMappedToMainIngress {
		if binding.Scheme == acaIngressSchemaHttp {
			return acaDefaultHttpPort, nil
		}
		if binding.Scheme == acaIngressSchemaHttps {
			return acaDefaultHttpsPort, nil
		}
	}
	if binding.TargetPort != nil {
		return fmt.Sprintf("%d", *binding.TargetPort), nil
	}
	// if the binding is not mapped to the main ingress and doesn't have a port defined, it uses the templated target port,
	// which is resolved on deployment time, after building the container (dotnet publish for project.v0)
	// dockerfile.v0 and container.v0 always have the target port defined in the binding.
	return acaTemplatedTargetPort, nil
}

// asYamlString converts a string to the YAML representation of the string, ensuring that it is quoted and escaped as needed.
func asYamlString(s string) (string, error) {
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
	yamlString, err := yaml.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("marshalling env value: %w", err)
	}

	// remove the trailing newline. yaml marshall will add a newline at the end of the string, as the new line is
	// expected at the end of the yaml document. But we are getting a single value with valid yaml here, so we don't
	// need the newline
	return string(yamlString[0 : len(yamlString)-1]), nil
}

func (b *infraGenerator) buildArgsBlock(args []string, manifestCtx *genContainerAppManifestTemplateContext) error {
	for argN, arg := range args {
		resolvedArg, err := EvalString(arg, func(s string) (string, error) { return b.evalBindingRef(s, inputEmitTypeYaml) })
		if err != nil {
			return fmt.Errorf("evaluating value for argument %d: %w", argN, err)
		}

		// Unlike environment variables, ACA doesn't provide a way to pass secret values without baking them into the args
		// array directly. We don't want folks to accidentally bake the plaintext value of these secrets into the container
		// definition, so for now, we block this.
		//
		// This logic is similar to what we do in buildEnvBlock to detect when we need to take values and treat them as ACA
		// secrets.
		if strings.Contains(arg, ".connectionString}") ||
			strings.Contains(resolvedArg, "{{ securedParameter ") ||
			strings.Contains(resolvedArg, "{{ secretOutput ") {

			return fmt.Errorf("argument %d cannot contain connection strings, secured parameters, or secret outputs. Use "+
				"environment variables instead", argN)
		}

		yamlString, err := asYamlString(resolvedArg)
		if err != nil {
			return fmt.Errorf("marshalling arg value: %w", err)
		}
		manifestCtx.Args = append(manifestCtx.Args, yamlString)
	}

	return nil
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

		resolvedValue, err := asYamlString(res)
		if err != nil {
			return fmt.Errorf("marshalling env value: %w", err)
		}

		// connectionString detection, either of:
		//  a) explicit connection string key for env, like "ConnectionStrings__resource": "XXXXX"
		//  b) a connection string field references in the value, like "FOO": "{resource.connectionString}"
		//  c) found placeholder for a secured-param, like "{{ securedParameter param }}"
		//  d) found placeholder for a secret output, like "{{ secretOutput kv secret }}"
		if strings.Contains(k, "ConnectionStrings__") || // a)
			strings.Contains(value, ".connectionString}") || // b)
			strings.Contains(resolvedValue, "{{ securedParameter ") || // c)
			strings.Contains(resolvedValue, "{{ secretOutput ") { // d)

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

// buildDeployBlock is like buildEnvBlock but supports additional conventions for referencing secrets
// It could be merged with buildEnvBlock, but it's kept separate for clarity until we have a better understanding of
// what the final implementation will look like.
func (b *infraGenerator) buildDeployBlock(
	deployParams map[string]any, manifestCtx *genContainerAppManifestTemplateContext) error {
	for k, valueAny := range deployParams {
		value, ok := valueAny.(string)
		if !ok {
			return fmt.Errorf("expected string value for %s, got %T", k, valueAny)
		}
		res, err := EvalString(value, func(s string) (string, error) { return b.evalBindingRef(s, inputEmitTypeYaml) })
		if err != nil {
			return fmt.Errorf("evaluating value for %s: %w", k, err)
		}

		resolvedValue, err := asYamlString(res)
		if err != nil {
			return fmt.Errorf("marshalling env value: %w", err)
		}
		if strings.Contains(k, "ConnectionStrings__") || // a)
			strings.Contains(value, ".connectionString}") || // b)
			strings.Contains(resolvedValue, "{{ securedParameter ") || // c)
			strings.Contains(resolvedValue, "{{ secretOutput ") { // d)

			// handle secret-outputs:
			// secretOutputs can be either complex expressions or direct references to key vault secrets.
			// A complex expression is like `key:{{ secretOutput kv secret }};foo;bar`.
			// For non complex expressions, like `{{ secretOutput kv secret }}`, the resolved value is set without the
			// secretOutput function. The caller can use the value as a reference to a key vault secret.
			// For complex expressions, the value includes the `secretOutput` function to pull the value during deployment.
			if strings.Contains(resolvedValue, "{{ secretOutput ") {
				if isComplexExp, _ := isComplexExpression(resolvedValue); !isComplexExp {
					removeBrackets := strings.ReplaceAll(
						strings.ReplaceAll(resolvedValue, " }}'", "'"), "{{ secretOutput ", "")
					resolvedValue = removeBrackets
				} else {
					resolvedValue = secretOutputForDeployTemplate(resolvedValue)
				}
			}
		}
		// make sure resolved value is quoted.
		// We can't ask EvalString() to quote strings b/c it depends on evalBindingRef() which can return complex
		// expressions where each part might be quoted or not.
		// Instead, before setting the deploy parameter, we just verify that it's quoted.
		if !strings.HasPrefix(resolvedValue, "'") {
			resolvedValue = "'" + resolvedValue + "'"
		}
		manifestCtx.DeployParams[k] = resolvedValue
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
