// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// hostProject keeps detection local to the extension instead of importing core's internal appdetect package.
type hostProject struct {
	path         string
	rootPath     string
	language     string
	frameworks   []string
	dockerfile   string
	ports        []int
	dependencies []string
}

func (p hostProject) hasWebUI() bool {
	for _, framework := range p.frameworks {
		if slices.Contains([]string{"react", "angular", "jquery", "vite"}, framework) {
			return true
		}
	}
	return false
}

func detectHostProject(ctx context.Context, path string) (hostProject, error) {
	project := hostProject{path: path}
	if err := ctx.Err(); err != nil {
		return project, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return project, err
	}
	files := map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			files[strings.ToLower(entry.Name())] = entry.Name()
		}
	}
	// Match core's language precedence without invoking build tools or executing application code.
	for name := range files {
		switch {
		case name == "pom.xml" || name == "build.gradle" || name == "build.gradle.kts":
			project.language = "java"
		case strings.HasSuffix(name, ".csproj") || strings.HasSuffix(name, ".fsproj") || strings.HasSuffix(name, ".vbproj"):
			if project.language != "java" {
				project.language = "dotnet"
			}
		}
	}
	if project.language == "" {
		switch {
		case files["pyproject.toml"] != "" || files["requirements.txt"] != "":
			project.language = "python"
			dependencyFile := files["pyproject.toml"]
			if dependencyFile == "" {
				dependencyFile = files["requirements.txt"]
			}
			//nolint:gosec // G304: the user selected this app directory; dependencyFile comes from its directory entries.
			contents, err := os.ReadFile(filepath.Join(path, dependencyFile))
			if err != nil {
				return project, err
			}
			project.dependencies = hostDependencyTypes(strings.FieldsFunc(string(contents), func(r rune) bool {
				return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
					(r >= '0' && r <= '9') || r == '-' || r == '_')
			}))
		case files["package.json"] != "":
			//nolint:gosec // G304: inspect the package manifest in the app directory selected by the user.
			contents, err := os.ReadFile(filepath.Join(path, files["package.json"]))
			if err != nil {
				return project, err
			}
			var manifest struct {
				Dependencies    map[string]string `json:"dependencies"`
				DevDependencies map[string]string `json:"devDependencies"`
			}
			if err := json.Unmarshal(contents, &manifest); err != nil {
				return project, fmt.Errorf("reading package.json: %w", err)
			}
			project.language = "js"
			var dependencies []string
			for dependency := range manifest.Dependencies {
				dependencies = append(dependencies, dependency)
				switch {
				case strings.HasPrefix(dependency, "@angular/"):
					project.frameworks = append(project.frameworks, "angular")
				case slices.Contains([]string{"react", "jquery", "vite", "next"}, dependency):
					project.frameworks = append(project.frameworks, dependency)
				}
			}
			if _, ok := manifest.DevDependencies["vite"]; ok && !slices.Contains(project.frameworks, "vite") {
				project.frameworks = append(project.frameworks, "vite")
			}
			if files["tsconfig.json"] != "" {
				project.language = "ts"
			} else if _, ok := manifest.Dependencies["typescript"]; ok {
				project.language = "ts"
			} else if _, ok := manifest.DevDependencies["typescript"]; ok {
				project.language = "ts"
			}
			project.dependencies = hostDependencyTypes(dependencies)
		}
	}
	slices.Sort(project.frameworks)
	project.frameworks = slices.Compact(project.frameworks)
	if project.language == "java" && files["pom.xml"] != "" {
		project.rootPath, err = hostMavenBuildRoot(ctx, path)
		if err != nil {
			return project, err
		}
	}
	if dockerfile := files["dockerfile"]; dockerfile != "" {
		project.dockerfile = filepath.Join(path, dockerfile)
		project.ports, err = detectHostDockerPorts(ctx, project.dockerfile)
	}
	return project, err
}

func hostMavenBuildRoot(ctx context.Context, path string) (string, error) {
	current := filepath.Clean(path)
	root := ""
	for directory := filepath.Dir(current); ; directory = filepath.Dir(directory) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		//nolint:gosec // G304: ancestor POMs identify the selected app's Maven reactor and sibling modules.
		contents, err := os.ReadFile(filepath.Join(directory, "pom.xml"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("reading Maven build root: %w", err)
		}
		if err == nil {
			var pom struct {
				Modules []string `xml:"modules>module"`
			}
			if err := xml.Unmarshal(contents, &pom); err != nil {
				return "", fmt.Errorf("reading Maven build root: %w", err)
			}
			for _, module := range pom.Modules {
				if filepath.Clean(filepath.Join(directory, strings.TrimSpace(module))) == current {
					// Keep the full reactor in the build context so module dependencies remain available.
					root, current = directory, directory
					break
				}
			}
		}
		if directory == filepath.Dir(directory) {
			return root, nil
		}
	}
}

func hostDependencyTypes(packages []string) []string {
	var types []string
	for _, name := range packages {
		var resourceType string
		switch strings.ToLower(name) {
		case "mongodb", "mongojs", "mongoose", "pymongo", "beanie", "motor":
			resourceType = "db.mongo"
		case "redis", "redis-om":
			resourceType = "db.redis"
		}
		if resourceType != "" && !slices.Contains(types, resourceType) {
			types = append(types, resourceType)
		}
	}
	slices.Sort(types)
	return types
}

func detectHostDockerPorts(ctx context.Context, path string) ([]int, error) {
	//nolint:gosec // G304: custom Dockerfiles may live outside the app directory; this path is explicitly user-selected.
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading Dockerfile: %w", err)
	}
	defer file.Close()

	var ports []int
	scanner := bufio.NewScanner(file)
	var instruction string
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		instruction += strings.TrimSuffix(line, "\\") + " "
		if strings.HasSuffix(line, "\\") {
			continue
		}
		fields := strings.Fields(instruction)
		instruction = ""
		if len(fields) == 0 {
			continue
		}
		if strings.EqualFold(fields[0], "FROM") {
			// Ports from intermediate build stages do not describe the image azd deploys.
			ports = nil
		}
		if !strings.EqualFold(fields[0], "EXPOSE") {
			continue
		}
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "#") {
				break
			}
			number, protocol, _ := strings.Cut(field, "/")
			if protocol != "" && !strings.EqualFold(protocol, "tcp") {
				continue
			}
			port, err := hostParsePort(number)
			// Variable-based EXPOSE instructions need a user-supplied port rather than a guessed value.
			if err == nil && !slices.Contains(ports, port) {
				ports = append(ports, port)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading Dockerfile: %w", err)
	}
	return ports, nil
}
