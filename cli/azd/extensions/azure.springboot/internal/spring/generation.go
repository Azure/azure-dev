// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

type databaseProvider struct {
	prefix string
	scheme string
}

var databaseProviders = map[ResourceKind]databaseProvider{
	MySQL:      {prefix: "MYSQL", scheme: "mysql"},
	PostgreSQL: {prefix: "POSTGRES", scheme: "postgresql"},
}

var containerAppName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$`)

//go:embed template/infra/*.bicep template/.azd-spring/prepare-database-password.ps1
//go:embed template/.azd-spring/bootstrap-postgresql-managed-identity.py template/.azd-spring/requirements.txt
var deploymentTemplate embed.FS

// Plan holds generated assets ready for a no-overwrite project write.
type Plan struct {
	Application *Application
	Files       map[string][]byte
}

// Generate configures the extension-owned azd template without core scaffold APIs.
func Generate(ctx context.Context, application *Application) (*Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(application.Requirements) > 1 {
		return nil, errors.New("multiple datasource bindings are not yet supported")
	}
	if application.Name == "" || !safeName.MatchString(application.Name) ||
		application.Port < 1 || application.Port > 65535 {
		return nil, errors.New("application name and port must be valid template inputs")
	}
	serviceName := application.Name + "-app"
	if !containerAppName.MatchString(serviceName) || strings.Contains(serviceName, "--") {
		return nil, errors.New("derived Container App name must be 2-32 lowercase letters, numbers, or hyphens, " +
			"start with a letter, end with a letter or number, and contain no consecutive hyphens; " +
			"choose a Maven artifactId whose normalized name fits these rules including the -app suffix")
	}
	hooks := map[string]any{}
	dockerPath := ".azd-spring/Dockerfile"
	if application.Dockerfile != "" {
		if application.Dockerfile != "Dockerfile" {
			return nil, errors.New("application Dockerfile must be the root Dockerfile")
		}
		dockerPath = application.Dockerfile
	}
	config := map[string]any{
		"name":  application.Name,
		"infra": map[string]string{"provider": "bicep", "path": "infra", "module": "main"},
		"services": map[string]any{serviceName: map[string]any{
			"project": ".", "host": "containerapp", "language": "docker", "module": "application",
			"docker": map[string]string{
				"path": dockerPath, "context": ".", "platform": "linux/amd64",
			},
		}},
	}
	parameters := map[string]any{}
	// #nosec G101 -- databasePassword is an azd environment substitution, not a hardcoded credential.
	for key, value := range map[string]any{
		"environmentName": "${AZURE_ENV_NAME}", "location": "${AZURE_LOCATION}",
		"serviceName": serviceName, "port": application.Port, "profiles": append([]string{}, application.Profiles...),
		"databaseEngine": "none", "databaseName": "", "databaseAuth": "password",
		"environmentBindings": application.Dockerfile != "",
		"jdbcOptions":         "", "driver": "",
		"embeddedUrl": "", "embeddedUrlConfigured": false, "embeddedDriver": "",
		"foundryEnabled":                 false,
		"foundryAuthentication":          "apiKey",
		"foundryChatEnabled":             false,
		"foundryChatDeploymentName":      "",
		"foundryChatModelName":           defaultFoundryChatModelName,
		"foundryChatModelVersion":        defaultFoundryChatModelVersion,
		"foundryChatModelSku":            "GlobalStandard",
		"foundryChatModelCapacity":       1,
		"foundryEmbeddingEnabled":        false,
		"foundryEmbeddingDeploymentName": "",
		"foundryEmbeddingModelName":      defaultFoundryEmbeddingModelName,
		"foundryEmbeddingModelVersion":   defaultFoundryEmbeddingModelVersion,
		"foundryEmbeddingModelSku":       "GlobalStandard",
		"foundryEmbeddingModelCapacity":  1,
		"databasePassword":               "${SPRING_DATABASE_PASSWORD=}",
		"administratorObjectId":          "${SPRING_DATABASE_ADMIN_OID=}",
		"administratorName":              "${SPRING_DATABASE_ADMIN_NAME=}",
		"appImage": "${SERVICE_" + strings.ToUpper(strings.ReplaceAll(serviceName, "-", "_")) +
			"_IMAGE_NAME=mcr.microsoft.com/azuredocs/containerapps-helloworld:latest}",
	} {
		parameters[key] = map[string]any{"value": value}
	}
	applicationValues := map[string]string{}
	applicationSecrets := map[string]any{}
	for _, input := range application.ApplicationInputs {
		switch input.Kind {
		case ApplicationInputValue:
			applicationValues[input.Name] = applicationInputReference(input.Name)
		case ApplicationInputSecret:
			applicationSecrets[applicationInputSecretName(input.Name)] = map[string]string{
				"name": input.Name, "value": applicationInputReference(input.Name),
			}
		default:
			return nil, fmt.Errorf("application input %s has unsupported kind %q", input.Name, input.Kind)
		}
	}
	parameters["applicationValues"] = map[string]any{"value": applicationValues}
	parameters["applicationSecrets"] = map[string]any{"value": applicationSecrets}
	settings := map[string]string{"engine": "none", "auth": "password", "database": "", "role": "azd_app"}
	var binding strings.Builder
	binding.WriteString("# Deployment-only overrides; application source is unchanged.\n")
	if application.Embedded != nil {
		if len(application.Requirements) != 0 {
			return nil, errors.New("an embedded datasource cannot also require an external database")
		}
		if err := validateEmbedded(application.Embedded); err != nil {
			return nil, err
		}
		if application.Embedded.URL != "" || application.Embedded.URLConfigured {
			fmt.Fprintf(&binding, "spring.datasource.url=%s\n", application.Embedded.URL)
		}
		if application.Embedded.Driver != "" {
			fmt.Fprintf(&binding, "spring.datasource.driver-class-name=%s\n", application.Embedded.Driver)
		}
		parameters["embeddedUrl"] = map[string]any{"value": application.Embedded.URL}
		parameters["embeddedUrlConfigured"] = map[string]any{"value": application.Embedded.URLConfigured}
		parameters["embeddedDriver"] = map[string]any{"value": application.Embedded.Driver}
	}
	for _, requirement := range application.Requirements {
		provider, ok := databaseProviders[requirement.Kind]
		if !ok {
			return nil, fmt.Errorf("no Azure provider is registered for resource kind %q", requirement.Kind)
		}
		if err := validateExternalDriver(requirement.Kind, requirement.Driver); err != nil {
			return nil, err
		}
		if !databaseName.MatchString(requirement.Name) {
			return nil, errors.New("database name is invalid")
		}
		if requirement.Authentication != "password" && requirement.Authentication != "managedIdentity" {
			return nil, errors.New("database authentication must be password or managedIdentity")
		}
		if requirement.Authentication == "managedIdentity" {
			if requirement.Kind != PostgreSQL {
				return nil, errors.New("MySQL managed identity is deferred pending live configuration validation")
			}
			if !application.IdentityExtensions {
				return nil, errors.New("managed identity requires com.azure:azure-identity-extensions " +
					"as an application compile/runtime dependency; initialization does not modify the POM")
			}
			hooks["preprovision"] = map[string]any{
				"shell":       "pwsh",
				"run":         "python .azd-spring/bootstrap-postgresql-managed-identity.py prepare",
				"interactive": false,
			}
			hooks["postprovision"] = map[string]any{
				"shell":       "pwsh",
				"run":         "python .azd-spring/bootstrap-postgresql-managed-identity.py bootstrap",
				"interactive": false,
			}
		} else {
			hooks["preprovision"] = map[string]any{
				"shell": "pwsh", "run": ".azd-spring/prepare-database-password.ps1", "interactive": false,
			}
		}
		parameters["databaseEngine"] = map[string]any{"value": string(requirement.Kind)}
		parameters["databaseName"] = map[string]any{"value": requirement.Name}
		parameters["databaseAuth"] = map[string]any{"value": requirement.Authentication}
		parameters["driver"] = map[string]any{"value": requirement.Driver}
		settings["engine"], settings["auth"], settings["database"] =
			string(requirement.Kind), requirement.Authentication, requirement.Name
		p := provider.prefix
		options := requirement.JDBCOptions
		if requirement.Authentication == "managedIdentity" {
			var err error
			options, err = managedIdentityOptions(options)
			if err != nil {
				return nil, err
			}
		}
		parameters["jdbcOptions"] = map[string]any{"value": options}
		fmt.Fprintf(&binding, "spring.datasource.url=jdbc:%s://${%s_HOST}:${%s_PORT}/${%s_DATABASE}?%s\n",
			provider.scheme, p, p, p, options)
		if requirement.Driver != "" {
			fmt.Fprintf(&binding, "spring.datasource.driver-class-name=%s\n", requirement.Driver)
		}
		fmt.Fprintf(&binding, "spring.datasource.username=${%s_USERNAME}\n", p)
		if requirement.Authentication == "managedIdentity" {
			binding.WriteString("spring.datasource.password=\n" +
				"spring.sql.init.mode=never\nspring.jpa.hibernate.ddl-auto=none\n")
		} else {
			fmt.Fprintf(&binding, "spring.datasource.password=${%s_PASSWORD}\n", p)
		}
	}
	if application.Foundry != nil {
		if !validFoundryDeploymentNames(application.Foundry) {
			return nil, errors.New("Foundry deployment names are invalid")
		}
		if application.Foundry.Authentication != "apiKey" &&
			application.Foundry.Authentication != "managedIdentity" {
			return nil, errors.New("Foundry authentication must be apiKey or managedIdentity")
		}
		parameters["foundryEnabled"] = map[string]any{"value": true}
		parameters["foundryAuthentication"] = map[string]any{"value": application.Foundry.Authentication}
		if application.Foundry.ChatDeploymentName != "" {
			parameters["foundryChatEnabled"] = map[string]any{"value": true}
			parameters["foundryChatDeploymentName"] =
				map[string]any{"value": application.Foundry.ChatDeploymentName}
		}
		if application.Foundry.EmbeddingDeploymentName != "" {
			parameters["foundryEmbeddingEnabled"] = map[string]any{"value": true}
			parameters["foundryEmbeddingDeploymentName"] =
				map[string]any{"value": application.Foundry.EmbeddingDeploymentName}
		}
		binding.WriteString("spring.ai.azure.openai.endpoint=${AZURE_OPENAI_ENDPOINT}\n")
		if application.Foundry.Authentication == "apiKey" {
			binding.WriteString("spring.ai.azure.openai.api-key=${AZURE_OPENAI_API_KEY}\n")
		}
		binding.WriteString("spring.ai.azure.openai.openai-api-key=\n")
		if application.Foundry.ChatDeploymentName != "" {
			binding.WriteString(
				"spring.ai.azure.openai.chat.options.deployment-name=${AZURE_OPENAI_CHAT_DEPLOYMENT_NAME}\n")
		}
		if application.Foundry.EmbeddingDeploymentName != "" {
			binding.WriteString(
				"spring.ai.azure.openai.embedding.options.deployment-name=" +
					"${AZURE_OPENAI_EMBEDDING_DEPLOYMENT_NAME}\n")
		}
	}
	if len(hooks) > 0 {
		config["hooks"] = hooks
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("serializing azd project: %w", err)
	}
	parameterData, err := json.MarshalIndent(map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0", "parameters": parameters,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serializing template parameters: %w", err)
	}
	applicationParameters := map[string]any{}
	for _, key := range []string{
		"location", "serviceName", "port", "profiles", "appImage", "databaseName",
		"environmentBindings", "jdbcOptions", "driver", "embeddedUrl", "embeddedUrlConfigured", "embeddedDriver",
		"foundryEnabled", "foundryAuthentication", "foundryChatEnabled", "foundryChatDeploymentName",
		"foundryEmbeddingEnabled", "foundryEmbeddingDeploymentName", "applicationValues", "applicationSecrets",
	} {
		applicationParameters[key] = parameters[key]
	}
	applicationParameters["engine"] = parameters["databaseEngine"]
	applicationParameters["authentication"] = parameters["databaseAuth"]
	applicationParameters["password"] = parameters["databasePassword"]
	username := "azdadmin"
	if settings["auth"] == "managedIdentity" {
		username = "azd_app"
	}
	for key, value := range map[string]string{
		"environmentId":    "${SPRING_CONTAINER_APP_ENVIRONMENT_ID}",
		"registryEndpoint": "${AZURE_CONTAINER_REGISTRY_ENDPOINT}",
		"host":             "${SPRING_DATABASE_HOST=}", "username": username,
		"foundryAccountName": "${SPRING_FOUNDRY_ACCOUNT_NAME=}",
	} {
		applicationParameters[key] = map[string]any{"value": value}
	}
	applicationParameterData, err := json.MarshalIndent(map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0", "parameters": applicationParameters,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serializing application deployment parameters: %w", err)
	}
	files := map[string][]byte{
		"azure.yaml":                             data,
		"infra/main.parameters.json":             parameterData,
		"infra/application.parameters.json":      applicationParameterData,
		".azd-spring/application-azd.properties": []byte(binding.String()),
	}
	if application.Dockerfile == "" {
		docker, err := dockerfile(application)
		if err != nil {
			return nil, err
		}
		ignore := ".git\n.azure\n**/target\n**/.env\n**/.env.*\n**/.idea\n/" +
			application.BuildDirectory + "\n"
		if application.JarDirectory != "" && application.JarDirectory != application.BuildDirectory {
			ignore += "/" + application.JarDirectory + "\n"
		}
		files[".azd-spring/Dockerfile"] = []byte(docker)
		files[".azd-spring/Dockerfile.dockerignore"] = []byte(ignore)
	} else {
		delete(files, ".azd-spring/application-azd.properties")
	}
	if settings["auth"] == "managedIdentity" {
		settingsData, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("serializing bootstrap settings: %w", err)
		}
		files[".azd-spring/database.json"] = settingsData
	}
	err = fs.WalkDir(deploymentTemplate, "template", func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		normalizedPath := strings.TrimPrefix(file, "template/")
		if normalizedPath == ".azd-spring/prepare-database-password.ps1" {
			if settings["engine"] == "none" || settings["auth"] != "password" {
				return nil
			}
		} else if strings.HasPrefix(normalizedPath, ".azd-spring/") && settings["auth"] != "managedIdentity" {
			return nil
		}
		content, err := fs.ReadFile(deploymentTemplate, file)
		if err != nil {
			return err
		}
		files[normalizedPath] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading deployment template: %w", err)
	}
	return &Plan{Application: application, Files: files}, nil
}

func applicationInputSecretName(name string) string {
	hash := sha256.Sum256([]byte(name))
	return fmt.Sprintf("app-%x", hash[:6])
}

func applicationInputReference(name string) string {
	return "${" + name + "}"
}

func validateOutputDirectory(directory, setting string, externalConfigurations []string) error {
	if !filepath.IsLocal(filepath.FromSlash(directory)) || directory == "." ||
		path.Clean(directory) != directory || strings.ContainsAny(directory, "\\*?[$\r\n") {
		return fmt.Errorf("%s must be a resolved project-local path without Docker patterns", setting)
	}
	for component := range strings.SplitSeq(directory, "/") {
		if strings.TrimSpace(component) != component {
			return fmt.Errorf("%s components cannot start or end with whitespace", setting)
		}
	}
	inputs := []string{
		"pom.xml", "mvnw", "mvnw.cmd", "src", "config", ".mvn", ".git", ".azure",
		"azure.yaml", "infra", ".azd-spring",
	}
	inputs = append(inputs, externalConfigurations...)
	for _, input := range inputs {
		if input == directory || strings.HasPrefix(input, directory+"/") ||
			strings.HasPrefix(directory, input+"/") {
			return fmt.Errorf("%s must not overlap application inputs or generated files", setting)
		}
	}
	return nil
}

func dockerfile(a *Application) (string, error) {
	if err := validateOutputDirectory(a.BuildDirectory, "Maven build.directory", a.ExternalConfigurations); err != nil {
		return "", err
	}
	jarDirectory := a.JarDirectory
	if jarDirectory == "" {
		jarDirectory = a.BuildDirectory
	}
	if err := validateOutputDirectory(jarDirectory,
		"Spring Boot repackage outputDirectory", a.ExternalConfigurations); err != nil {
		return "", err
	}
	jarPaths, err := json.Marshal([]string{path.Join("/workspace", jarDirectory, a.JarName), "/app/application.jar"})
	if err != nil {
		return "", fmt.Errorf("encoding Maven JAR paths: %w", err)
	}
	build := "RUN mvn -B -Dmaven.test.skip=true package"
	if a.MavenWrapper {
		build = `RUN sed -i 's/\r$//' mvnw \
    && chmod +x mvnw \
    && MAVEN_CONFIG= ./mvnw -B -Dmaven.test.skip=true package`
	}
	var external strings.Builder
	for _, file := range a.ExternalConfigurations {
		paths, err := json.Marshal([]string{"/workspace/" + file, "/app/" + file})
		if err != nil {
			return "", fmt.Errorf("encoding external configuration paths: %w", err)
		}
		fmt.Fprintf(&external, "COPY --from=build --chown=10001:10001 %s\n", paths)
	}
	return fmt.Sprintf(`FROM maven:3.9-eclipse-temurin-%d AS build
WORKDIR /workspace
COPY . .
%s

FROM eclipse-temurin:%d-jre
WORKDIR /app
COPY --from=build --chown=10001:10001 %s
%sCOPY --chown=10001:10001 .azd-spring/application-azd.properties /app/.azd-spring/application.properties
EXPOSE %d
USER 10001
ENTRYPOINT ["java", "-jar", "/app/application.jar"]
`, a.JavaVersion, build, a.JavaVersion, jarPaths, external.String(), a.Port), nil
}

// CheckDestination rejects existing azd ownership and incompatible Docker inputs before a trusted build.
func CheckDestination(directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range []string{"azure.yaml", "azure.yml", "infra", ".azd-spring"} {
		if _, err := root.Lstat(name); err == nil {
			return fmt.Errorf("%s already exists; "+
				"initialization does not overwrite or adopt existing deployment files", name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("checking initialization destination: %w", err)
		}
	}
	if _, err := root.Lstat(".dockerignore"); err == nil {
		if _, err := root.Lstat("Dockerfile"); errors.Is(err, fs.ErrNotExist) {
			return errors.New("a root .dockerignore requires an application-owned root Dockerfile; " +
				"the generated Dockerfile-specific ignore file would bypass its exclusions")
		} else if err != nil {
			return fmt.Errorf("checking root Dockerfile: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking root .dockerignore: %w", err)
	}
	return nil
}

// Write creates only new files under the application root and rolls them back on failure.
func (p *Plan) Write(ctx context.Context, directory string) (resultErr error) {
	if err := CheckDestination(directory); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := p.validateDockerfileState(root); err != nil {
		return err
	}
	names := slices.Sorted(maps.Keys(p.Files))
	for _, name := range names {
		if !fs.ValidPath(name) || name != "azure.yaml" &&
			!strings.HasPrefix(name, "infra/") && !strings.HasPrefix(name, ".azd-spring/") {
			return errors.New("generation plan contains an invalid output path")
		}
	}
	var createdFiles, createdDirs []string
	defer func() {
		if resultErr == nil {
			return
		}
		for _, name := range slices.Backward(createdFiles) {
			if err := root.Remove(name); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("rolling back generated file %s: %w", name, err))
			}
		}
		for _, name := range slices.Backward(createdDirs) {
			if err := root.Remove(name); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("rolling back generated directory %s: %w", name, err))
			}
		}
	}()
	// Commit azure.yaml last, after every referenced asset has been written.
	names = slices.DeleteFunc(names, func(name string) bool { return name == "azure.yaml" })
	if _, present := p.Files["azure.yaml"]; present {
		names = append(names, "azure.yaml")
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent := path.Dir(name)
		if parent != "." {
			parts := strings.Split(parent, "/")
			for i := range len(parts) {
				dir := strings.Join(parts[:i+1], "/")
				if info, err := root.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
					if err := root.Mkdir(dir, 0o755); err != nil {
						return fmt.Errorf("creating generation directory: %w", err)
					}
					createdDirs = append(createdDirs, dir)
				} else if err != nil {
					return fmt.Errorf("checking generation directory: %w", err)
				} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return errors.New("generation directories must not be symbolic links")
				}
			}
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("creating generated file %s: %w", name, err)
		}
		createdFiles = append(createdFiles, name)
		_, writeErr := file.Write(p.Files[name])
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return fmt.Errorf("writing generated file %s: %w", name, err)
		}
	}
	return nil
}

func (p *Plan) validateDockerfileState(root *os.Root) error {
	expected := p.Application != nil && p.Application.Dockerfile == "Dockerfile"
	info, err := root.Lstat("Dockerfile")
	if errors.Is(err, fs.ErrNotExist) {
		if expected {
			return errors.New("Dockerfile changed after analysis; rerun initialization")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking Dockerfile before writing: %w", err)
	}
	if !expected {
		return errors.New("Dockerfile appeared after analysis; rerun initialization")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("the root Dockerfile must remain a regular file")
	}
	return nil
}
