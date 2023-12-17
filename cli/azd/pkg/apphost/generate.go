package apphost

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/psanford/memfs"
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
				"bicepName":        scaffold.BicepName,
				"alphaSnakeUpper":  scaffold.AlphaSnakeUpper,
				"containerAppName": scaffold.ContainerAppName,
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
				Path:     *comp.Path,
				Context:  *comp.Context,
				Env:      comp.Env,
				Bindings: comp.Bindings,
			}
		}
	}

	return res
}

// ContainerAppManifestTemplateForProject returns the container app manifest template for a given project.
// It can be used (after evaluation) to deploy the service to a container app environment.
func ContainerAppManifestTemplateForProject(manifest *Manifest, projectName string) (string, error) {
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

	fs := memfs.New()

	if err := executeToFS(fs, genTemplates, "main.bicep", "main.bicep", generator.bicepContext); err != nil {
		return nil, fmt.Errorf("generating infra/main.bicep: %w", err)
	}

	if err := executeToFS(fs, genTemplates, "resources.bicep", "resources.bicep", generator.bicepContext); err != nil {
		return nil, fmt.Errorf("generating infra/resources.bicep: %w", err)
	}

	if err := executeToFS(fs, genTemplates, "main.parameters.json", "main.parameters.json", nil); err != nil {
		return nil, fmt.Errorf("generating infra/resources.bicep: %w", err)
	}

	return fs, nil
}

// GenerateProjectArtifacts generates all the artifacts to manage a project with `azd`. Te azure.yaml file as well as
// a helpful next-steps.md file.
func GenerateProjectArtifacts(
	ctx context.Context, projectDir string, projectName string, manifest *Manifest, appHostProject string,
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
	resourceTypes     map[string]string

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
			DaprComponents:                  make(map[string]genDaprComponent),
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

// LoadManifest loads the given manifest into the generator. It should be called before [Compile].
func (b *infraGenerator) LoadManifest(m *Manifest) error {
	for name, comp := range m.Resources {
		b.resourceTypes[name] = comp.Type

		switch comp.Type {
		case "azure.servicebus.v0":
			b.addServiceBus(name, comp.Queues, comp.Topics)
		case "azure.appinsights.v0":
			b.addAppInsights(name)
		case "project.v0":
			b.addProject(name, *comp.Path, comp.Env, comp.Bindings)
		case "container.v0":
			b.addContainer(name, *comp.Image, comp.Env, comp.Bindings)
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
			b.addDockerfile(name, *comp.Path, *comp.Context, comp.Env, comp.Bindings)
		case "redis.v0":
			b.addContainerAppService(name, RedisContainerAppService)
		case "azure.keyvault.v0":
			b.addKeyVault(name)
		case "azure.storage.v0":
			b.addStorageAccount(name)
		case "azure.storage.blob.v0":
			b.addStorageBlob(*comp.Parent, name)
		case "azure.storage.queue.v0":
			b.addStorageQueue(*comp.Parent, name)
		case "azure.storage.table.v0":
			b.addStorageTable(*comp.Parent, name)
		case "postgres.server.v0":
			// We currently use a ACA Postgres Service per database. Because of this, we don't need to retain any
			// information from the server resource.
			//
			// We have the case statement here to ensure we don't error out on the resource type by treating it as an unknown
			// resource type.
		case "postgres.database.v0":
			b.addContainerAppService(name, "postgres")
		case "postgres.connection.v0":
			b.connectionStrings[name] = *comp.ConnectionString
		case "rabbitmq.connection.v0":
			b.connectionStrings[name] = *comp.ConnectionString
		case "azure.cosmosdb.connection.v0":
			b.connectionStrings[name] = *comp.ConnectionString
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
	b.bicepContext.HasContainerEnvironment = true
}

func (b *infraGenerator) requireContainerRegistry() {
	b.requireLogAnalyticsWorkspace()
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

func (b *infraGenerator) addServiceBus(name string, queues, topics *[]string) {
	if queues == nil {
		queues = &[]string{}
	}

	if topics == nil {
		topics = &[]string{}
	}
	b.bicepContext.ServiceBuses[name] = genServiceBus{Queues: *queues, Topics: *topics}
}

func (b *infraGenerator) addAppInsights(name string) {
	b.requireLogAnalyticsWorkspace()
	b.bicepContext.AppInsights[name] = genAppInsight{}
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
	b.bicepContext.StorageAccounts[name] = genStorageAccount{}
}

func (b *infraGenerator) addKeyVault(name string) {
	b.bicepContext.KeyVaults[name] = genKeyVault{}
}

func (b *infraGenerator) addStorageBlob(storageAccount, blobName string) {
	// TODO(ellismg): We have to handle the case where we may visit the blob resource before the storage account resource.
	// But this implementation means that if the parent storage account is not in the manifest, we will not detect that
	// as an error.  We probably should.

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

func (b *infraGenerator) addContainer(name string, image string, env map[string]string, bindings map[string]*Binding) {
	b.requireCluster()

	b.containers[name] = genContainer{
		Image:    image,
		Env:      env,
		Bindings: bindings,
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
	name string, path string, context string, env map[string]string, bindings map[string]*Binding,
) {
	b.requireCluster()
	b.requireContainerRegistry()

	b.dockerfiles[name] = genDockerfile{
		Path:     path,
		Context:  context,
		Env:      env,
		Bindings: bindings,
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

// Compile compiles the loaded manifest into the internal representation used to generate the infrastructure files. Once
// called the context objects on the infraGenerator can be passed to the text templates to generate the required
// infrastructure.
func (b *infraGenerator) Compile() error {
	for name, container := range b.containers {
		cs := genContainerApp{
			Image: container.Image,
		}

		ingress, err := buildIngress(container.Bindings)
		if err != nil {
			return fmt.Errorf("configuring ingress for resource %s: %w", name, err)
		}

		cs.Ingress = ingress

		b.bicepContext.ContainerApps[name] = cs
	}

	for resourceName, docker := range b.dockerfiles {
		projectTemplateCtx := genContainerAppManifestTemplateContext{
			Name: resourceName,
			Env:  make(map[string]string),
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
			Name: resourceName,
			Env:  make(map[string]string),
		}

		binding, err := validateAndMergeBindings(project.Bindings)
		if err != nil {
			return fmt.Errorf("configuring ingress for project %s: %w", resourceName, err)
		}

		if binding != nil {
			projectTemplateCtx.Ingress = &genContainerAppIngress{
				External:  binding.External,
				Transport: binding.Transport,

				// TODO(ellismg): We need to inspect the target container and determine this from the exposed ports (or ask
				// MSBuild to tell us this value when it builds the container image). For now we just assume 8080.
				//
				// We can get this by running `dotnet publish` and using the `--getProperty:GeneratedContainerConfiguration`
				// flag to get the generated docker configuration.  That's a JSON object, from that we pluck off
				// Config.ExposedPorts, which is an object that would look like:
				//
				// {
				//    "8080/tcp": {}
				// }
				//
				// Note that the protocol type is apparently optional.
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

	return nil
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

// evalBindingRef evaluates a binding reference expression based on the state of the manifest loaded into the generator.
func (b infraGenerator) evalBindingRef(v string) (string, error) {
	parts := strings.SplitN(v, ".", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("malformed binding expression, expected <resourceName>.<propertyPath> but was: %s", v)
	}

	resource, prop := parts[0], parts[1]
	targetType, ok := b.resourceTypes[resource]
	if !ok {
		return "", fmt.Errorf("unknown resource referenced in binding expression: %s", resource)
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
			return "", fmt.Errorf("malformed binding expression, expected bindings.<binding-name>.[port|url] but was: %s", v)
		}
	case targetType == "postgres.database.v0" || targetType == "redis.v0":
		switch prop {
		case "connectionString":
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
	case targetType == "azure.cosmosdb.connection.v0" ||
		targetType == "postgres.connection.v0" ||
		targetType == "rabbitmq.connection.v0":

		switch prop {
		case "connectionString":
			return b.connectionStrings[resource], nil
		default:
			return "", errUnsupportedProperty(targetType, prop)
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
// evaluating any binding expressions that are present.
func (b *infraGenerator) buildEnvBlock(env map[string]string, manifestCtx *genContainerAppManifestTemplateContext) error {
	for k, value := range env {
		res, err := evalString(value, b.evalBindingRef)
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
		jsonStr, err := yaml.Marshal(res)
		if err != nil {
			return fmt.Errorf("marshalling env value: %w", err)
		}

		manifestCtx.Env[k] = string(jsonStr)
	}

	return nil
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
