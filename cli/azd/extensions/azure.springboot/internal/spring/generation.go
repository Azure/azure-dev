// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

type databaseProvider struct {
	resource project.ResourceType
	prefix   string
	scheme   string
}

var databaseProviders = map[ResourceKind]databaseProvider{
	MySQL:      {resource: project.ResourceTypeDbMySql, prefix: "MYSQL", scheme: "mysql"},
	PostgreSQL: {resource: project.ResourceTypeDbPostgres, prefix: "POSTGRES", scheme: "postgresql"},
}

// Plan holds generated assets ready for a no-overwrite project write.
type Plan struct {
	Application *Application
	Files       map[string][]byte
}

// Generate reuses azd's project serializer and resource-based infrastructure generator.
func Generate(ctx context.Context, application *Application) (_ *Plan, resultErr error) {
	if len(application.Requirements) > 1 {
		return nil, errors.New("multiple datasource bindings are not yet supported")
	}
	stage, err := os.MkdirTemp("", "azd-spring-generation-")
	if err != nil {
		return nil, fmt.Errorf("creating generation staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("removing generation staging directory: %w", err))
		}
	}()
	serviceName := application.Name + "-app"
	config := &project.ProjectConfig{
		Name:      application.Name,
		Infra:     provisioning.Options{Provider: provisioning.Bicep, Path: "infra", Module: "main"},
		Services:  map[string]*project.ServiceConfig{},
		Resources: map[string]*project.ResourceConfig{},
	}
	service := &project.ServiceConfig{
		Project: config, Name: serviceName, RelativePath: ".", Host: project.ContainerAppTarget,
		Language: project.ServiceLanguageDocker,
		Docker:   project.DockerProjectOptions{Path: ".azd-spring/Dockerfile", Context: ".", Platform: "linux/amd64"},
	}
	config.Services[serviceName] = service
	env := []project.ServiceEnvVar{
		{Name: "SPRING_PROFILES_ACTIVE", Value: strings.Join(application.Profiles, ",")},
		{Name: "SPRING_CONFIG_ADDITIONAL_LOCATION", Value: "file:/app/config/application-azd.properties"},
		{Name: "SERVER_PORT", Value: strconv.Itoa(application.Port)},
	}
	var binding strings.Builder
	binding.WriteString("# Deployment-only overrides. Credentials are supplied through Container Apps secrets.\n")
	for _, requirement := range application.Requirements {
		provider, ok := databaseProviders[requirement.Kind]
		if !ok {
			return nil, fmt.Errorf("no Azure provider is registered for resource kind %q", requirement.Kind)
		}
		if requirement.Authentication != "password" {
			return nil, errors.New("the database provider currently supports password authentication only")
		}
		if _, duplicate := config.Resources[requirement.Name]; duplicate || requirement.Name == serviceName {
			return nil, errors.New("resource names must be unique across the generated deployment")
		}
		config.Resources[requirement.Name] = &project.ResourceConfig{Type: provider.resource, Name: requirement.Name}
		service.Uses = append(service.Uses, requirement.Name)
		p := provider.prefix
		fmt.Fprintf(&binding, "spring.datasource.url=jdbc:%s://${%s_HOST}:${%s_PORT}/${%s_DATABASE}?%s\n",
			provider.scheme, p, p, p, requirement.JDBCOptions)
		fmt.Fprintf(&binding, "spring.datasource.username=${%s_USERNAME}\nspring.datasource.password=${%s_PASSWORD}\n", p, p)
	}
	config.Resources[serviceName] = &project.ResourceConfig{
		Type: project.ResourceTypeHostContainerApp, Name: serviceName, Uses: service.Uses,
		Props: project.ContainerAppProps{Port: application.Port, Env: env},
	}
	file := filepath.Join(stage, "azure.yaml")
	if err := project.Save(ctx, config, file); err != nil {
		return nil, fmt.Errorf("serializing azd project: %w", err)
	}
	data, err := fs.ReadFile(os.DirFS(stage), "azure.yaml")
	if err != nil {
		return nil, fmt.Errorf("reading staged azd project: %w", err)
	}
	// Parsing initializes the same runtime resource/service relationships as azd.
	normalized, err := project.Parse(ctx, string(data))
	if err != nil {
		return nil, fmt.Errorf("validating serialized azd project: %w", err)
	}
	normalized.Path = stage
	infrastructure, err := project.NewImportManager(nil).GenerateAllInfrastructure(ctx, normalized)
	if err != nil {
		return nil, fmt.Errorf("generating azd infrastructure: %w", err)
	}
	docker, err := dockerfile(application)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		"azure.yaml":                             data,
		".azd-spring/Dockerfile":                 []byte(docker),
		".azd-spring/application-azd.properties": []byte(binding.String()),
		".azd-spring/Dockerfile.dockerignore": []byte(
			".git\n.azure\n**/target\n**/.env\n**/.env.*\n**/.idea\n**/*credentials*\n"),
	}
	err = fs.WalkDir(infrastructure, ".", func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		normalizedPath := filepath.ToSlash(file)
		if !fs.ValidPath(normalizedPath) || !strings.HasPrefix(normalizedPath, "infra/") {
			return fmt.Errorf("azd generator returned an unexpected infrastructure path %q", file)
		}
		content, err := fs.ReadFile(infrastructure, file)
		if err != nil {
			return err
		}
		files[normalizedPath] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading generated infrastructure: %w", err)
	}
	return &Plan{Application: application, Files: files}, nil
}

func dockerfile(a *Application) (string, error) {
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
COPY --from=build --chown=10001:10001 /workspace/target/%s /app/application.jar
%sCOPY --chown=10001:10001 .azd-spring/application-azd.properties /app/config/application-azd.properties
EXPOSE %d
USER 10001
ENTRYPOINT ["java", "-jar", "/app/application.jar"]
`, a.JavaVersion, build, a.JavaVersion, a.JarName, external.String(), a.Port), nil
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
