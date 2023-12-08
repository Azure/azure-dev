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
	"strconv"
	"strings"
	"text/template"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/resources"
	"github.com/psanford/memfs"
)

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
func BicepTemplate(manifest *Manifest) (fs.FS, error) {
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
		},
		containers:                   make(map[string]genContainer),
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
		case "redis.v0":
			b.addContainerAppService(name, "redis")
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
		binding, err := validateAndMergeBindings(container.Bindings)
		if err != nil {
			return fmt.Errorf("validating binding for %s resource: %w", name, err)
		}

		cs := genContainerApp{
			Image: container.Image,
		}

		if binding != nil {
			if binding.ContainerPort == nil {
				return fmt.Errorf(
					"binding for %s resource does not specify a container port, "+
						"ensure WithServiceBinding for this resource specifies a hostPort value", name)
			}

			cs.Ingress = &genContainerServiceIngress{
				External:      binding.External,
				TargetPort:    *binding.ContainerPort,
				Transport:     binding.Transport,
				AllowInsecure: strings.ToLower(binding.Transport) == "http2" || !binding.External,
			}
		}

		b.bicepContext.ContainerApps[name] = cs
	}

	for projectName, project := range b.projects {
		projectTemplateCtx := genContainerAppManifestTemplateContext{
			Name: projectName,
			Env:  make(map[string]string),
		}

		binding, err := validateAndMergeBindings(project.Bindings)
		if err != nil {
			return fmt.Errorf("configuring ingress for project %s: %w", projectName, err)
		}

		if binding != nil {
			projectTemplateCtx.Ingress = &genContainerAppManifestTemplateContextIngress{
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

		for k, v := range project.Env {
			if !strings.HasPrefix(v, "{") || !strings.HasSuffix(v, "}") {
				// We want to ensure that we render these values in the YAML as strings.  If `v` was the string "false"
				// (without the quotes), we would naturally create a value directive in yaml that looks like this:
				//
				// - name: OTEL_DOTNET_EXPERIMENTAL_OTLP_EMIT_EXCEPTION_LOG_ATTRIBUTES
				//   value: true
				//
				// And YAML rules would treat the above as the value being a boolean instead of a string, which the container
				// app service expects.
				//
				// JSON marshalling the string value will give us something like `"true"` (with the quotes, and any escaping
				// that needs to be done), which is what we want here.
				jsonStr, err := json.Marshal(v)
				if err != nil {
					return fmt.Errorf("marshalling env value: %w", err)
				}

				projectTemplateCtx.Env[k] = string(jsonStr)
				continue
			}

			parts := strings.SplitN(v[1:len(v)-1], ".", 2)
			if len(parts) != 2 {
				return fmt.Errorf("malformed binding expression, expected <resourceName>.<propertyPath> but was: %s", v)
			}

			resource, prop := parts[0], parts[1]
			targetType, ok := b.resourceTypes[resource]
			if !ok {
				return fmt.Errorf("unknown resource referenced in binding expression: %s", resource)
			}

			switch {
			case targetType == "project.v0" || targetType == "container.v0":
				if !strings.HasPrefix(prop, "bindings.") {
					return fmt.Errorf("unsupported property referenced in binding expression: %s for %s", prop, targetType)
				}

				parts := strings.Split(prop[len("bindings."):], ".")
				if len(parts) != 2 || parts[1] != "url" {
					return fmt.Errorf("malformed binding expression, expected bindings.<binding-name>.url but was: %s", v)
				}

				var binding *Binding
				var has bool

				if targetType == "project.v0" {
					binding, has = b.projects[resource].Bindings[parts[0]]
				} else if targetType == "container.v0" {
					binding, has = b.containers[resource].Bindings[parts[0]]
				}

				if !has {
					return fmt.Errorf(
						"unknown binding referenced in binding expression: %s for resource %s", parts[0], resource)
				}

				if binding.External {
					projectTemplateCtx.Env[k] = fmt.Sprintf(
						"%s://%s.{{ .Env.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN }}", binding.Scheme, resource)
				} else {
					projectTemplateCtx.Env[k] = fmt.Sprintf(
						"%s://%s.internal.{{ .Env.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN }}", binding.Scheme, resource)
				}
			case targetType == "postgres.database.v0" || targetType == "redis.v0":
				switch prop {
				case "connectionString":
					projectTemplateCtx.Env[k] = fmt.Sprintf(`{{ connectionString "%s" }}`, resource)
				default:
					return errUnsupportedProperty(targetType, prop)
				}
			case targetType == "azure.servicebus.v0":
				switch prop {
				case "connectionString":
					projectTemplateCtx.Env[k] = fmt.Sprintf(
						"{{ urlHost .Env.SERVICE_BINDING_%s_ENDPOINT }}", scaffold.AlphaSnakeUpper(resource))
				default:
					return errUnsupportedProperty("azure.servicebus.v0", prop)
				}
			case targetType == "azure.appinsights.v0":
				switch prop {
				case "connectionString":
					projectTemplateCtx.Env[k] = fmt.Sprintf(
						"{{ .Env.SERVICE_BINDING_%s_CONNECTION_STRING }}", scaffold.AlphaSnakeUpper(resource))
				default:
					return errUnsupportedProperty("azure.appinsights.v0", prop)
				}
			case targetType == "azure.cosmosdb.connection.v0" ||
				targetType == "postgres.connection.v0" ||
				targetType == "rabbitmq.connection.v0":

				switch prop {
				case "connectionString":
					projectTemplateCtx.Env[k] = b.connectionStrings[resource]
				default:
					return errUnsupportedProperty(targetType, prop)
				}
			case targetType == "azure.keyvault.v0" ||
				targetType == "azure.storage.blob.v0" ||
				targetType == "azure.storage.queue.v0" ||
				targetType == "azure.storage.table.v0":
				switch prop {
				case "connectionString":
					projectTemplateCtx.Env[k] = fmt.Sprintf(
						"{{ .Env.SERVICE_BINDING_%s_ENDPOINT }}", scaffold.AlphaSnakeUpper(resource))
				default:
					return errUnsupportedProperty(targetType, prop)
				}
			default:
				ignore, err := strconv.ParseBool(os.Getenv("AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES"))
				if err == nil && ignore {
					log.Printf("ignoring binding reference to resource of type %s since "+
						"AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES is set", targetType)

					projectTemplateCtx.Env[k] = fmt.Sprintf(
						"!!! expression '%s' to type '%s' unsupported by azd !!!", v, targetType)
					continue
				}

				return fmt.Errorf("unsupported resource type %s referenced in binding expression", targetType)
			}
		}

		b.containerAppTemplateContexts[projectName] = projectTemplateCtx
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
