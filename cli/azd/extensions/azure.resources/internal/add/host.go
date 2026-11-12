// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"azure.resources/internal/resources"
)

func (w *Wizard) configureHost(
	ctx context.Context,
	resourceType string,
) (name string, definition resources.Definition, service map[string]any, err error) {
	if resourceType != resources.ContainerApp && resourceType != resources.AppService {
		return "", definition, nil, fmt.Errorf("unsupported host type: %s", resourceType)
	}

	project, err := w.hostCodeProject(ctx)
	if err != nil {
		return "", definition, nil, err
	}
	if resourceType == resources.AppService {
		if err := validateHostAppService(project); err != nil {
			return "", definition, nil, err
		}
	}

	for {
		name, err = w.resourceName(ctx, w.hostDefaultName(project.path))
		if err != nil {
			return "", definition, nil, err
		}
		if err := w.validateHostName(name); err != nil {
			w.message(err.Error())
			continue
		}
		break
	}

	if resourceType == resources.ContainerApp && project.dockerfile == "" {
		automatic, err := w.confirm(ctx, "No Dockerfile found. Allow azd to automatically build a container image?", true)
		if err != nil {
			return "", definition, nil, err
		}
		if !automatic {
			for {
				path, err := w.text(ctx, "Where is your Dockerfile located?", "")
				if err != nil {
					return "", definition, nil, err
				}
				path, err = hostDockerfilePath(w.ProjectDir, path)
				if err != nil {
					w.message(err.Error())
					continue
				}
				ports, err := detectHostDockerPorts(ctx, path)
				if err != nil {
					if ctx.Err() != nil {
						return "", definition, nil, ctx.Err()
					}
					w.message(err.Error())
					continue
				}
				project.dockerfile, project.ports = path, ports
				break
			}
		}
	}

	port, err := w.hostPort(ctx, name, project)
	if err != nil {
		return "", definition, nil, err
	}
	service, err = hostServiceConfig(w.ProjectDir, name, resourceType, project)
	if err != nil {
		return "", definition, nil, err
	}
	definition = resources.Definition{Type: resourceType, Port: port}
	if resourceType == resources.AppService {
		definition.Runtime, err = hostRuntime(project.language)
		if err != nil {
			return "", resources.Definition{}, nil, err
		}
		w.message(fmt.Sprintf("azd will use %s %s to host this project on Azure App Service.",
			definition.Runtime.Stack, definition.Runtime.Version))
	}
	if len(project.dependencies) > 0 {
		w.message("Detected resource dependencies: " + strings.Join(project.dependencies, ", ") +
			". Select the matching resources when configuring links.")
	}
	return name, definition, service, nil
}

func (w *Wizard) hostCodeProject(ctx context.Context) (hostProject, error) {
	for {
		value, err := w.text(ctx, "Where is your app code project located?", ".")
		if err != nil {
			return hostProject{}, err
		}
		path, err := hostCodeDirectory(w.ProjectDir, value)
		if err != nil {
			w.message(err.Error())
			continue
		}
		project, err := detectHostProject(ctx, path)
		if err != nil {
			return hostProject{}, fmt.Errorf("detecting project: %w", err)
		}
		if project.language == "" {
			w.message("Could not automatically detect language.")
			selection, err := w.choose(ctx, "Enter the language or framework", []string{
				".NET", "Java", "JavaScript", "Python", "TypeScript", "Angular", "JQuery", "React", "Vite",
			})
			if err != nil {
				return hostProject{}, err
			}
			project.language, project.frameworks, err = hostManualLanguage(selection)
			if err != nil {
				return hostProject{}, err
			}
			if project.language == "python" {
				if _, err := os.Stat(filepath.Join(path, "requirements.txt")); errors.Is(err, os.ErrNotExist) {
					return hostProject{}, fmt.Errorf(
						"no requirements.txt found in %q; run 'pip freeze > requirements.txt' in your app directory", path)
				} else if err != nil {
					return hostProject{}, err
				}
			}
		}
		return project, nil
	}
}

func hostCodeDirectory(root, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("provide the directory containing your app code")
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving project directory: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("reading project directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", value)
	}
	return path, nil
}

func hostDockerfilePath(root, value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("provide the path to your Dockerfile")
	}
	path := value
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving Dockerfile: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("reading Dockerfile: %w", err)
	}
	if info.IsDir() {
		path = filepath.Join(path, "Dockerfile")
		info, err = os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("reading Dockerfile: %w", err)
		}
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular Dockerfile", path)
	}
	return path, nil
}

func (w *Wizard) validateHostName(name string) error {
	if name == "" {
		return fmt.Errorf("service name is required")
	}
	if err := resources.ValidateEntryName(name); err != nil {
		return err
	}
	if strings.HasSuffix(name, "-") {
		return fmt.Errorf("service name must end with a lowercase letter or digit")
	}
	for existing := range w.Services {
		if resources.ServiceKey(name) == resources.ServiceKey(existing) {
			return fmt.Errorf("service name %q conflicts with existing service %q", name, existing)
		}
	}
	for existing := range w.Resources {
		if resources.ServiceKey(name) == resources.ServiceKey(existing) {
			return fmt.Errorf("service name %q conflicts with existing resource %q", name, existing)
		}
	}
	return nil
}

func (w *Wizard) hostDefaultName(path string) string {
	var label strings.Builder
	for _, char := range strings.ToLower(filepath.Base(path)) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			label.WriteRune(char)
		} else if label.Len() > 0 && !strings.HasSuffix(label.String(), "-") {
			label.WriteByte('-')
		}
	}
	base := strings.Trim(label.String(), "-")
	if base == "" || base[0] < 'a' || base[0] > 'z' {
		base = "service-" + base
	}
	base = strings.TrimRight(base[:min(len(base), 40)], "-")
	name := base
	for suffix := 2; w.validateHostName(name) != nil; suffix++ {
		tail := "-" + strconv.Itoa(suffix)
		name = strings.TrimRight(base[:min(len(base), 40-len(tail))], "-") + tail
	}
	return name
}

func hostManualLanguage(selection string) (string, []string, error) {
	switch selection {
	case ".NET":
		return "dotnet", nil, nil
	case "Java":
		return "java", nil, nil
	case "JavaScript":
		return "js", nil, nil
	case "Python":
		return "python", nil, nil
	case "TypeScript":
		return "ts", nil, nil
	case "Angular", "JQuery", "React", "Vite":
		return "js", []string{strings.ToLower(selection)}, nil
	default:
		return "", nil, fmt.Errorf("unsupported language or framework: %s", selection)
	}
}

func validateHostAppService(project hostProject) error {
	if project.dockerfile != "" {
		return fmt.Errorf("App Service with custom containers is not supported by add; use Container Apps instead")
	}
	if project.hasWebUI() {
		return fmt.Errorf("App Service deployment for static site applications is not supported by add")
	}
	_, err := hostRuntime(project.language)
	return err
}

func hostRuntime(language string) (resources.AppServiceRuntime, error) {
	switch language {
	case "python":
		return resources.AppServiceRuntime{Stack: "python", Version: "3.13"}, nil
	case "js", "ts":
		return resources.AppServiceRuntime{Stack: "node", Version: "22-lts"}, nil
	default:
		return resources.AppServiceRuntime{}, fmt.Errorf(
			"App Service does not support language %q with add; use Container Apps instead", language)
	}
}

func hostServiceConfig(root, name, resourceType string, project hostProject) (map[string]any, error) {
	relative, err := filepath.Rel(root, project.path)
	if err != nil {
		return nil, fmt.Errorf("resolving service project path: %w", err)
	}
	host := strings.TrimPrefix(resourceType, "host.")
	key := resources.ServiceKey(name)
	service := map[string]any{
		"project": filepath.ToSlash(relative), "language": project.language, "host": host,
		"resourceGroup": "${SERVICE_" + key + "_RESOURCE_GROUP}",
		"resourceName":  "${SERVICE_" + key + "_NAME}",
	}
	if resourceType == resources.ContainerApp {
		// Each infrastructure entry has its own registry, so the global registry default is not sufficient.
		docker := map[string]any{"registry": "${SERVICE_" + key + "_REGISTRY_ENDPOINT}"}
		if project.dockerfile != "" {
			path, err := filepath.Rel(project.path, project.dockerfile)
			if err != nil {
				return nil, fmt.Errorf("resolving Dockerfile path: %w", err)
			}
			docker["path"] = filepath.ToSlash(path)
		}
		if project.rootPath != "" {
			buildContext, err := filepath.Rel(project.path, project.rootPath)
			if err != nil {
				return nil, fmt.Errorf("resolving Docker build context: %w", err)
			}
			docker["context"] = filepath.ToSlash(buildContext)
		}
		service["docker"] = docker
	}
	if project.hasWebUI() {
		dist := "dist"
		switch {
		case slices.Contains(project.frameworks, "next"):
			dist = ""
		case slices.Contains(project.frameworks, "vite"):
		case slices.Contains(project.frameworks, "angular"):
			dist = "dist/" + filepath.Base(project.path)
		case slices.Contains(project.frameworks, "react"):
			dist = "build"
		}
		if dist != "" {
			service["dist"] = dist
		}
	}
	return service, nil
}

func (w *Wizard) hostPort(ctx context.Context, name string, project hostProject) (int, error) {
	if project.dockerfile == "" {
		if project.language == "java" || project.language == "dotnet" {
			return 8080, nil
		}
		return 80, nil
	}
	if len(project.ports) == 1 {
		return project.ports[0], nil
	}
	message := "What port does '" + name + "' listen on?"
	if len(project.ports) > 1 {
		choices := make([]string, 0, len(project.ports)+1)
		for _, port := range project.ports {
			choices = append(choices, strconv.Itoa(port))
		}
		choices = append(choices, "Other")
		value, err := w.choose(ctx, message, choices)
		if err != nil {
			return 0, err
		}
		if value != "Other" {
			return hostParsePort(value)
		}
	}
	for {
		value, err := w.text(ctx, message, "")
		if err != nil {
			return 0, err
		}
		port, err := hostParsePort(value)
		if err != nil {
			w.message(err.Error())
			continue
		}
		return port, nil
	}
}

func hostParsePort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be an integer between 1 and 65535")
	}
	return port, nil
}
