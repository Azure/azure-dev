// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.resources/internal/resources"

	"github.com/stretchr/testify/require"
)

func hostTestDirectory(t *testing.T, files map[string]string) string {
	t.Helper()
	path, err := os.MkdirTemp(".", ".host-test-")
	require.NoError(t, err)
	path, err = filepath.Abs(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(path)) })
	for name, contents := range files {
		target := filepath.Join(path, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o750))
		require.NoError(t, os.WriteFile(target, []byte(contents), 0o600))
	}
	return path
}

func TestHostCodeDirectory(t *testing.T) {
	root := hostTestDirectory(t, map[string]string{"app/package.json": "{}"})
	for _, tt := range []struct {
		name  string
		value string
		valid bool
	}{
		{"root", ".", true},
		{"relative", "app", true},
		{"absolute", filepath.Join(root, "app"), true},
		{"empty", "", false},
		{"missing", "missing", false},
		{"file", "app/package.json", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path, err := hostCodeDirectory(root, tt.value)
			if !tt.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.True(t, filepath.IsAbs(path))
		})
	}
}

func TestHostDependencyTypesIgnoresRemovedDatabases(t *testing.T) {
	require.Empty(t, hostDependencyTypes([]string{"pg", "psycopg2-binary", "mysql2", "mysqlclient"}))
}

func TestDetectHostProject(t *testing.T) {
	for _, tt := range []struct {
		name         string
		files        map[string]string
		language     string
		frameworks   []string
		dependencies []string
	}{
		{"unknown", map[string]string{"main.go": "package main"}, "", nil, nil},
		{"python", map[string]string{"requirements.txt": "fastapi\npymongo>=4\nredis>=5"},
			"python", nil, []string{resources.Mongo, resources.Redis}},
		{"python pyproject", map[string]string{"pyproject.toml": "[project]\ndependencies = [\"pymongo>=4\"]"},
			"python", nil, []string{resources.Mongo}},
		{"javascript", map[string]string{"package.json": `{"dependencies":{"express":"1","mongodb":"1","mongoose":"1"}}`},
			"js", nil, []string{resources.Mongo}},
		{"typescript vite", map[string]string{
			"package.json": `{"dependencies":{"react":"1"},"devDependencies":{"vite":"1","typescript":"1"}}`,
		}, "ts", []string{"react", "vite"}, nil},
		{"typescript config", map[string]string{"package.json": "{}", "tsconfig.json": "{}"}, "ts", nil, nil},
		{"angular", map[string]string{"package.json": `{"dependencies":{"@angular/core":"1","@angular/forms":"1"}}`},
			"js", []string{"angular"}, nil},
		{"dotnet", map[string]string{"app.csproj": "<Project />"}, "dotnet", nil, nil},
		{"fsharp", map[string]string{"app.fsproj": "<Project />"}, "dotnet", nil, nil},
		{"java maven", map[string]string{"pom.xml": "<project />"}, "java", nil, nil},
		{"java gradle", map[string]string{"build.gradle.kts": ""}, "java", nil, nil},
		{"precedence", map[string]string{"app.csproj": "", "pom.xml": "", "package.json": "{}"}, "java", nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := hostTestDirectory(t, tt.files)
			project, err := detectHostProject(t.Context(), root)
			require.NoError(t, err)
			require.Equal(t, root, project.path)
			require.Equal(t, tt.language, project.language)
			require.Equal(t, tt.frameworks, project.frameworks)
			require.Equal(t, tt.dependencies, project.dependencies)
		})
	}
}

func TestDetectHostProjectErrors(t *testing.T) {
	root := hostTestDirectory(t, map[string]string{"package.json": "not json"})
	_, err := detectHostProject(t.Context(), root)
	require.ErrorContains(t, err, "package.json")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = detectHostProject(ctx, root)
	require.ErrorIs(t, err, context.Canceled)
}

func TestHostManualLanguage(t *testing.T) {
	for _, tt := range []struct {
		choice   string
		language string
		webUI    bool
	}{
		{".NET", "dotnet", false}, {"Java", "java", false}, {"JavaScript", "js", false},
		{"Python", "python", false}, {"TypeScript", "ts", false}, {"Angular", "js", true},
		{"JQuery", "js", true}, {"React", "js", true}, {"Vite", "js", true},
	} {
		t.Run(tt.choice, func(t *testing.T) {
			language, frameworks, err := hostManualLanguage(tt.choice)
			require.NoError(t, err)
			require.Equal(t, tt.language, language)
			require.Equal(t, tt.webUI, (hostProject{frameworks: frameworks}).hasWebUI())
		})
	}
	_, _, err := hostManualLanguage("unsupported")
	require.Error(t, err)
}

func TestHostAppServiceRuntime(t *testing.T) {
	for _, tt := range []struct {
		language string
		stack    string
		version  string
	}{
		{"python", "python", "3.13"}, {"js", "node", "22-lts"}, {"ts", "node", "22-lts"},
	} {
		t.Run(tt.language, func(t *testing.T) {
			runtime, err := hostRuntime(tt.language)
			require.NoError(t, err)
			require.Equal(t, resources.AppServiceRuntime{Stack: tt.stack, Version: tt.version}, runtime)
		})
	}
	for _, project := range []hostProject{
		{language: "dotnet"}, {language: "java"},
		{language: "python", dockerfile: "Dockerfile"}, {language: "js", frameworks: []string{"react"}},
	} {
		require.Error(t, validateHostAppService(project))
	}
	require.NoError(t, validateHostAppService(hostProject{language: "python"}))
}

func TestHostNames(t *testing.T) {
	w := Wizard{
		Resources: resources.Config{"api": {Type: resources.ContainerApp}},
		Services:  map[string]map[string]any{"api-2": {}, "my-api": {}},
	}
	require.Equal(t, "api-3", w.hostDefaultName(filepath.Join("project", "API")))
	require.Equal(t, "my-new-app", w.hostDefaultName(filepath.Join("project", "My New App!")))
	require.Equal(t, "service-123", w.hostDefaultName(filepath.Join("project", "123")))
	long := w.hostDefaultName(strings.Repeat("a", 80))
	require.Len(t, long, 40)
	require.NoError(t, w.validateHostName(long))
	for _, invalid := range []string{"", "../app", "App", "api", "api-2", "my-api", "my--api", "trailing-"} {
		require.Error(t, w.validateHostName(invalid), invalid)
	}
}

func TestHostServiceConfig(t *testing.T) {
	root := hostTestDirectory(t, nil)
	path := filepath.Join(root, "apps", "web")
	project := hostProject{
		path: path, language: "ts", dockerfile: filepath.Join(path, "docker", "Dockerfile"),
		frameworks: []string{"react", "vite"},
	}
	service, err := hostServiceConfig(root, "web-api", resources.ContainerApp, project)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"project": "apps/web", "language": "ts", "host": "containerapp", "dist": "dist",
		"resourceGroup": "${SERVICE_WEB_API_RESOURCE_GROUP}",
		"resourceName":  "${SERVICE_WEB_API_NAME}",
		"docker": map[string]any{
			"registry": "${SERVICE_WEB_API_REGISTRY_ENDPOINT}", "path": "docker/Dockerfile",
		},
	}, service)

	project = hostProject{path: root, language: "python"}
	service, err = hostServiceConfig(root, "api", resources.AppService, project)
	require.NoError(t, err)
	require.Equal(t, ".", service["project"])
	require.Equal(t, "appservice", service["host"])
	require.NotContains(t, service, "docker")
	require.NotContains(t, service, "dist")
}

func TestHostFrameworkDist(t *testing.T) {
	root := hostTestDirectory(t, nil)
	for _, tt := range []struct {
		frameworks []string
		dist       string
	}{
		{[]string{"react"}, "build"}, {[]string{"jquery"}, "dist"},
		{[]string{"angular"}, "dist/web"}, {[]string{"react", "vite"}, "dist"},
		{[]string{"react", "next"}, ""},
	} {
		service, err := hostServiceConfig(root, "web", resources.ContainerApp, hostProject{
			path: filepath.Join(root, "web"), language: "js", frameworks: tt.frameworks,
		})
		require.NoError(t, err)
		if tt.dist == "" {
			require.NotContains(t, service, "dist")
		} else {
			require.Equal(t, tt.dist, service["dist"])
		}
	}
}

func TestHostDockerPorts(t *testing.T) {
	for _, tt := range []struct {
		name  string
		text  string
		ports []int
	}{
		{"one", "FROM base\nEXPOSE 8080\n", []int{8080}},
		{"multiple", "FROM base\nexpose 80 443/tcp \\\n 8080\nEXPOSE 80\n", []int{80, 443, 8080}},
		{"final stage", "FROM base AS build\nEXPOSE 1234\nFROM base\nEXPOSE 8000\n", []int{8000}},
		{"unknown", "FROM base\nEXPOSE ${PORT} 0 65536 53/udp # 9000\n", nil},
		{"none", "FROM base\n", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := hostTestDirectory(t, map[string]string{"Dockerfile": tt.text})
			ports, err := detectHostDockerPorts(t.Context(), filepath.Join(path, "Dockerfile"))
			require.NoError(t, err)
			require.Equal(t, tt.ports, ports)
			project, err := detectHostProject(t.Context(), path)
			require.NoError(t, err)
			require.Equal(t, ports, project.ports)
			require.Equal(t, filepath.Join(path, "Dockerfile"), project.dockerfile)
		})
	}
}

func TestHostPortDefaultsAndValidation(t *testing.T) {
	w := Wizard{}
	for language, expected := range map[string]int{"python": 80, "js": 80, "ts": 80, "java": 8080, "dotnet": 8080} {
		port, err := w.hostPort(t.Context(), "api", hostProject{language: language})
		require.NoError(t, err)
		require.Equal(t, expected, port)
	}
	port, err := w.hostPort(t.Context(), "api", hostProject{dockerfile: "Dockerfile", ports: []int{5000}})
	require.NoError(t, err)
	require.Equal(t, 5000, port)
	for _, value := range []string{"0", "-1", "65536", "text", "80.5", ""} {
		_, err := hostParsePort(value)
		require.Error(t, err, value)
	}
	for _, value := range []string{"1", "80", "65535"} {
		_, err := hostParsePort(value)
		require.NoError(t, err)
	}
}

func TestHostDockerfilePath(t *testing.T) {
	root := hostTestDirectory(t, map[string]string{
		"docker/Dockerfile": "FROM base", "docker/Dockerfile.custom": "FROM base",
	})
	for _, input := range []string{"docker", "docker/Dockerfile", filepath.Join(root, "docker")} {
		path, err := hostDockerfilePath(root, input)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(root, "docker", "Dockerfile"), path)
	}
	path, err := hostDockerfilePath(root, "docker/Dockerfile.custom")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "docker", "Dockerfile.custom"), path)
	for _, input := range []string{"", ".", "missing"} {
		_, err := hostDockerfilePath(root, input)
		require.Error(t, err)
	}
}

func TestConfigureHost(t *testing.T) {
	for _, tt := range []struct {
		name       string
		files      map[string]string
		host       string
		prompts    wizardPromptServer
		language   string
		port       int
		dockerfile string
		message    string
	}{
		{
			name: "automatic container image", files: map[string]string{"app/package.json": "{}"},
			host: resources.ContainerApp, prompts: wizardPromptServer{textAnswers: []string{"app", "api"}},
			language: "js", port: 80,
		},
		{
			name: "App Service runtime", files: map[string]string{"app/requirements.txt": "flask"},
			host: resources.AppService, prompts: wizardPromptServer{textAnswers: []string{"app", "api"}},
			language: "python", port: 80, message: "python 3.13",
		},
		{
			name: "directory and name retries", files: map[string]string{"app/package.json": "{}"},
			host: resources.ContainerApp, prompts: wizardPromptServer{
				textAnswers: []string{"missing", "app", "taken", "other--service", "api"},
			},
			language: "js", port: 80, message: "conflicts with existing service",
		},
		{
			name: "manual language fallback", files: map[string]string{"app/README.md": "app"},
			host: resources.ContainerApp, prompts: wizardPromptServer{
				textAnswers: []string{"app", "api"}, selectAnswers: []string{"TypeScript"},
			},
			language: "ts", port: 80, message: "Could not automatically detect language.",
		},
		{
			name: "custom Dockerfile directory", files: map[string]string{
				"app/package.json": "{}", "docker/Dockerfile": "FROM base\nEXPOSE 5000",
			},
			host: resources.ContainerApp, prompts: wizardPromptServer{
				textAnswers: []string{"app", "api", "missing", "docker"}, confirmAnswers: []bool{false},
			},
			language: "js", port: 5000, dockerfile: "../docker/Dockerfile",
		},
		{
			name: "port retries", files: map[string]string{"app/package.json": "{}", "app/Dockerfile": "FROM base"},
			host: resources.ContainerApp, prompts: wizardPromptServer{
				textAnswers: []string{"app", "api", "abc", "0", "65536", "5000"},
			},
			language: "js", port: 5000, dockerfile: "Dockerfile", message: "port must be an integer",
		},
		{
			name: "multiple exposed ports", files: map[string]string{
				"app/package.json": "{}", "app/Dockerfile": "FROM base\nEXPOSE 80 443",
			},
			host: resources.ContainerApp, prompts: wizardPromptServer{
				textAnswers: []string{"app", "api"}, selectAnswers: []string{"443"},
			},
			language: "js", port: 443, dockerfile: "Dockerfile",
		},
		{
			name: "other exposed port", files: map[string]string{
				"app/package.json": "{}", "app/Dockerfile": "FROM base\nEXPOSE 80 443",
			},
			host: resources.ContainerApp, prompts: wizardPromptServer{
				textAnswers: []string{"app", "api", "3000"}, selectAnswers: []string{"Other"},
			},
			language: "js", port: 3000, dockerfile: "Dockerfile",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := hostTestDirectory(t, tt.files)
			var writer bytes.Buffer
			w := Wizard{
				Client: newWizardTestClient(t, &tt.prompts), Writer: &writer, ProjectDir: root,
				Resources: resources.Config{"taken": {Type: resources.Storage}},
				Services:  map[string]map[string]any{"other-service": {"host": "containerapp"}},
			}
			name, definition, service, err := w.configureHost(t.Context(), tt.host)
			require.NoError(t, err)
			require.Equal(t, "api", name)
			require.Equal(t, tt.host, definition.Type)
			require.Equal(t, tt.port, definition.Port)
			require.NoError(t, (resources.Config{name: definition}).Validate())
			require.Equal(t, tt.language, service["language"])
			require.Equal(t, "app", service["project"])
			require.Equal(t, "${SERVICE_API_RESOURCE_GROUP}", service["resourceGroup"])
			require.Equal(t, "${SERVICE_API_NAME}", service["resourceName"])
			if tt.host == resources.ContainerApp {
				docker, ok := service["docker"].(map[string]any)
				require.True(t, ok)
				require.Equal(t, "${SERVICE_API_REGISTRY_ENDPOINT}", docker["registry"])
				if tt.dockerfile != "" {
					require.Equal(t, tt.dockerfile, docker["path"])
				} else {
					require.NotContains(t, docker, "path")
				}
			}
			if tt.message != "" {
				require.Contains(t, writer.String(), tt.message)
			}
			require.Empty(t, tt.prompts.textAnswers)
			require.Empty(t, tt.prompts.selectAnswers)
			require.Empty(t, tt.prompts.confirmAnswers)
			require.Equal(t, resources.Config{"taken": {Type: resources.Storage}}, w.Resources)
			require.Equal(t, map[string]map[string]any{"other-service": {"host": "containerapp"}}, w.Services)
		})
	}
}

func TestConfigureHostRejectsUnsupportedAppService(t *testing.T) {
	for _, files := range []map[string]string{
		{"app/package.json": "{}", "app/Dockerfile": "FROM base"},
		{"app/package.json": `{"dependencies":{"react":"1"}}`},
		{"app/app.csproj": ""},
		{"app/pom.xml": ""},
	} {
		root := hostTestDirectory(t, files)
		prompts := wizardPromptServer{textAnswers: []string{"app"}}
		w := Wizard{Client: newWizardTestClient(t, &prompts), ProjectDir: root}
		_, _, _, err := w.configureHost(t.Context(), resources.AppService)
		require.ErrorContains(t, err, "not support")
		require.Empty(t, prompts.textAnswers)
	}
}

func TestConfigureHostPromptFailure(t *testing.T) {
	prompts := wizardPromptServer{promptErr: errors.New("prompt failed")}
	w := Wizard{Client: newWizardTestClient(t, &prompts)}
	_, _, _, err := w.configureHost(t.Context(), resources.ContainerApp)
	require.ErrorContains(t, err, "prompt failed")

	_, _, _, err = w.configureHost(t.Context(), resources.Storage)
	require.ErrorContains(t, err, "unsupported host type")
}

func TestHostMavenBuildContext(t *testing.T) {
	for _, tt := range []struct {
		name      string
		files     map[string]string
		project   string
		buildRoot string
		context   string
	}{
		{
			name: "sibling dependency module",
			files: map[string]string{
				"pom.xml":         `<project><modules><module>api</module><module>library</module></modules></project>`,
				"api/pom.xml":     `<project><parent><relativePath>../pom.xml</relativePath></parent></project>`,
				"api/Dockerfile":  "FROM base\nEXPOSE 8080",
				"library/pom.xml": "<project />",
			},
			project: "api", buildRoot: ".", context: "..",
		},
		{
			name: "nested reactor",
			files: map[string]string{
				"pom.xml": `<project><modules><module>services</module>` +
					`<module>library</module></modules></project>`,
				"services/pom.xml":     `<project><modules><module>api</module></modules></project>`,
				"services/api/pom.xml": "<project />",
				"library/pom.xml":      "<project />",
			},
			project: "services/api", buildRoot: ".", context: "../..",
		},
		{
			name: "unrelated parent POM",
			files: map[string]string{
				"pom.xml":     `<project><modules><module>other</module></modules></project>`,
				"api/pom.xml": "<project />",
			},
			project: "api",
		},
		{
			name:    "standalone",
			files:   map[string]string{"api/pom.xml": "<project />"},
			project: "api",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := hostTestDirectory(t, tt.files)
			project, err := detectHostProject(t.Context(), filepath.Join(root, filepath.FromSlash(tt.project)))
			require.NoError(t, err)
			require.Equal(t, "java", project.language)
			if tt.buildRoot == "" {
				require.Empty(t, project.rootPath)
			} else {
				require.Equal(t, filepath.Join(root, tt.buildRoot), project.rootPath)
			}
			service, err := hostServiceConfig(root, "api", resources.ContainerApp, project)
			require.NoError(t, err)
			docker, ok := service["docker"].(map[string]any)
			require.True(t, ok)
			if tt.context == "" {
				require.NotContains(t, docker, "context")
			} else {
				require.Equal(t, tt.context, docker["context"])
			}
			if project.dockerfile != "" {
				require.Equal(t, "Dockerfile", docker["path"])
			}
		})
	}
}
