// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
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

//go:embed template/infra/*.bicep template/.azd-spring/database.ps1
//go:embed template/.azd-spring/database.py template/.azd-spring/requirements.txt
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
	config := map[string]any{
		"name":  application.Name,
		"infra": map[string]string{"provider": "bicep", "path": "infra", "module": "main"},
		"services": map[string]any{serviceName: map[string]any{
			"project": ".", "host": "containerapp", "language": "docker", "module": "application",
			"docker": map[string]string{
				"path": ".azd-spring/Dockerfile", "context": ".", "platform": "linux/amd64",
			},
		}},
	}
	parameters := map[string]any{}
	// #nosec G101 -- databasePassword is an azd environment substitution, not a hardcoded credential.
	for key, value := range map[string]any{
		"environmentName": "${AZURE_ENV_NAME}", "location": "${AZURE_LOCATION}",
		"serviceName": serviceName, "port": application.Port, "profiles": append([]string{}, application.Profiles...),
		"databaseEngine": "none", "databaseName": "", "databaseAuth": "password",
		"databasePassword":      "${SPRING_DATABASE_PASSWORD=}",
		"administratorObjectId": "${SPRING_DATABASE_ADMIN_OID=}",
		"administratorName":     "${SPRING_DATABASE_ADMIN_NAME=}",
		"bootstrapIp":           "${SPRING_DATABASE_BOOTSTRAP_IP=}",
		"appImage": "${SERVICE_" + strings.ToUpper(strings.ReplaceAll(serviceName, "-", "_")) +
			"_IMAGE_NAME=mcr.microsoft.com/azuredocs/containerapps-helloworld:latest}",
	} {
		parameters[key] = map[string]any{"value": value}
	}
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
	}
	for _, requirement := range application.Requirements {
		provider, ok := databaseProviders[requirement.Kind]
		if !ok {
			return nil, fmt.Errorf("no Azure provider is registered for resource kind %q", requirement.Kind)
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
				"shell": "pwsh", "run": "python .azd-spring/database.py prepare", "interactive": false,
			}
			hooks["postprovision"] = map[string]any{
				"shell": "pwsh", "run": "python .azd-spring/database.py bootstrap", "interactive": false,
			}
		} else {
			hooks["preprovision"] = map[string]any{
				"shell": "pwsh", "run": ".azd-spring/database.ps1", "interactive": false,
			}
		}
		parameters["databaseEngine"] = map[string]any{"value": string(requirement.Kind)}
		parameters["databaseName"] = map[string]any{"value": requirement.Name}
		parameters["databaseAuth"] = map[string]any{"value": requirement.Authentication}
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
		fmt.Fprintf(&binding, "spring.datasource.url=jdbc:%s://${%s_HOST}:${%s_PORT}/${%s_DATABASE}?%s\n",
			provider.scheme, p, p, p, options)
		fmt.Fprintf(&binding, "spring.datasource.username=${%s_USERNAME}\n", p)
		if requirement.Authentication == "managedIdentity" {
			binding.WriteString("spring.datasource.password=\n" +
				"spring.datasource.hikari.data-source-properties.sslrootcert=/etc/ssl/certs/ca-certificates.crt\n" +
				"spring.sql.init.mode=never\nspring.jpa.hibernate.ddl-auto=none\n")
		} else {
			fmt.Fprintf(&binding, "spring.datasource.password=${%s_PASSWORD}\n", p)
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
	for _, key := range []string{"location", "serviceName", "port", "profiles", "appImage", "databaseName"} {
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
	docker, err := dockerfile(application)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		"azure.yaml":                             data,
		"infra/main.parameters.json":             parameterData,
		"infra/application.parameters.json":      applicationParameterData,
		".azd-spring/Dockerfile":                 []byte(docker),
		".azd-spring/application-azd.properties": []byte(binding.String()),
		".azd-spring/Dockerfile.dockerignore": []byte(
			".git\n.azure\n**/target\n**/.env\n**/.env.*\n**/.idea\n**/*credentials*\n/" +
				application.BuildDirectory + "\n"),
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
		if normalizedPath == ".azd-spring/database.ps1" {
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

func dockerfile(a *Application) (string, error) {
	if !filepath.IsLocal(filepath.FromSlash(a.BuildDirectory)) || a.BuildDirectory == "." ||
		path.Clean(a.BuildDirectory) != a.BuildDirectory || strings.ContainsAny(a.BuildDirectory, "\\*?[$\r\n") {
		return "", errors.New("Maven build.directory must be a resolved project-local path without Docker patterns")
	}
	for component := range strings.SplitSeq(a.BuildDirectory, "/") {
		if strings.TrimSpace(component) != component {
			return "", errors.New("Maven build.directory components cannot start or end with whitespace")
		}
	}
	inputs := []string{"pom.xml", "mvnw", "mvnw.cmd", "src", "config", ".mvn", ".git", ".azure", ".azd-spring"}
	inputs = append(inputs, a.ExternalConfigurations...)
	for _, input := range inputs {
		if input == a.BuildDirectory || strings.HasPrefix(input, a.BuildDirectory+"/") ||
			strings.HasPrefix(a.BuildDirectory, input+"/") {
			return "", errors.New("Maven build.directory must not overlap application inputs or generated files")
		}
	}
	jarPaths, err := json.Marshal([]string{path.Join("/workspace", a.BuildDirectory, a.JarName), "/app/application.jar"})
	if err != nil {
		return "", fmt.Errorf("encoding Maven JAR paths: %w", err)
	}
	build := "RUN mvn -B -DskipTests package"
	if a.MavenWrapper {
		build = "RUN chmod +x mvnw && ./mvnw -B -DskipTests package"
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
%sCOPY --chown=10001:10001 .azd-spring/application-azd.properties /app/config/application-azd.properties
EXPOSE %d
USER 10001
ENTRYPOINT ["java", "-jar", "/app/application.jar"]
`, a.JavaVersion, build, a.JavaVersion, jarPaths, external.String(), a.Port), nil
}

// CheckDestination refuses existing azd/container ownership before executing a trusted build.
func CheckDestination(directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range []string{"azure.yaml", "infra", ".azd-spring", "Dockerfile"} {
		if _, err := root.Lstat(name); err == nil {
			return fmt.Errorf("%s already exists; "+
				"initialization does not overwrite or adopt existing deployment files", name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("checking initialization destination: %w", err)
		}
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
