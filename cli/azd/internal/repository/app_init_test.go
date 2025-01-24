package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitializer_prjConfigFromDetect(t *testing.T) {
	tests := []struct {
		name         string
		detect       detectConfirm
		interactions []string
		want         project.ProjectConfig
	}{
		{
			name: "api",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.DotNet,
						Path:     "dotnet",
					},
				},
			},
			interactions: []string{},
			want: project.ProjectConfig{
				Services: map[string]*project.ServiceConfig{
					"dotnet": {
						Language:     project.ServiceLanguageDotNet,
						RelativePath: "dotnet",
						Host:         project.ContainerAppTarget,
					},
				},
				Resources: map[string]*project.ResourceConfig{
					"dotnet": {
						Type: project.ResourceTypeHostContainerApp,
						Name: "dotnet",
						Props: project.ContainerAppProps{
							Port: 8080,
						},
					},
				},
			},
		},
		{
			name: "web",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.JavaScript,
						Path:     "js",
						Dependencies: []appdetect.Dependency{
							appdetect.JsReact,
						},
					},
				},
			},
			interactions: []string{},
			want: project.ProjectConfig{
				Services: map[string]*project.ServiceConfig{
					"js": {
						Language:     project.ServiceLanguageJavaScript,
						Host:         project.ContainerAppTarget,
						RelativePath: "js",
						OutputPath:   "build",
					},
				},
				Resources: map[string]*project.ResourceConfig{
					"js": {
						Type: project.ResourceTypeHostContainerApp,
						Name: "js",
						Props: project.ContainerAppProps{
							Port: 80,
						},
					},
				},
			},
		},
		{
			name: "api with docker",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.DotNet,
						Path:     "dotnet",
						Docker:   &appdetect.Docker{Path: "Dockerfile"},
					},
				},
			},
			interactions: []string{
				// prompt for port -- hit multiple validation cases
				"notAnInteger",
				"-2",
				"65536",
				"1234",
			},
			want: project.ProjectConfig{
				Services: map[string]*project.ServiceConfig{
					"dotnet": {
						Language:     project.ServiceLanguageDotNet,
						Host:         project.ContainerAppTarget,
						RelativePath: "dotnet",
						Docker: project.DockerProjectOptions{
							Path: "Dockerfile",
						},
					},
				},
				Resources: map[string]*project.ResourceConfig{
					"dotnet": {
						Type: project.ResourceTypeHostContainerApp,
						Name: "dotnet",
						Props: project.ContainerAppProps{
							Port: 1234,
						},
					},
				},
			},
		},
		{
			name: "api and web",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.Python,
						Path:     "py",
					},
					{
						Language: appdetect.JavaScript,
						Path:     "js",
						Dependencies: []appdetect.Dependency{
							appdetect.JsReact,
						},
					},
				},
			},
			interactions: []string{},
			want: project.ProjectConfig{
				Services: map[string]*project.ServiceConfig{
					"py": {
						Language:     project.ServiceLanguagePython,
						Host:         project.ContainerAppTarget,
						RelativePath: "py",
					},
					"js": {
						Language:     project.ServiceLanguageJavaScript,
						Host:         project.ContainerAppTarget,
						RelativePath: "js",
						OutputPath:   "build",
					},
				},

				Resources: map[string]*project.ResourceConfig{
					"py": {
						Type: project.ResourceTypeHostContainerApp,
						Name: "py",
						Props: project.ContainerAppProps{
							Port: 80,
						},
					},
					"js": {
						Type: project.ResourceTypeHostContainerApp,
						Name: "js",
						Uses: []string{"py"},
						Props: project.ContainerAppProps{
							Port: 80,
						},
					},
				},
			},
		},
		{
			name: "full",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.Python,
						Path:     "py",
						DatabaseDeps: []appdetect.DatabaseDep{
							appdetect.DbPostgres,
							appdetect.DbMongo,
							appdetect.DbRedis,
						},
					},
					{
						Language: appdetect.JavaScript,
						Path:     "js",
						Dependencies: []appdetect.Dependency{
							appdetect.JsReact,
						},
					},
				},
				Databases: map[appdetect.DatabaseDep]EntryKind{
					appdetect.DbPostgres: EntryKindDetected,
					appdetect.DbMongo:    EntryKindDetected,
					appdetect.DbRedis:    EntryKindDetected,
				},
			},
			interactions: []string{
				// prompt for db -- hit multiple validation cases
				"my app db",
				"n",
				"my$special$db",
				"n",
				"mongodb", // fill in db name
				// prompt for db -- hit multiple validation cases
				"my$special$db",
				"n",
				"postgres", // fill in db name
			},
			want: project.ProjectConfig{
				Services: map[string]*project.ServiceConfig{
					"py": {
						Language:     project.ServiceLanguagePython,
						Host:         project.ContainerAppTarget,
						RelativePath: "py",
					},
					"js": {
						Language:     project.ServiceLanguageJavaScript,
						Host:         project.ContainerAppTarget,
						RelativePath: "js",
						OutputPath:   "build",
					},
				},
				Resources: map[string]*project.ResourceConfig{
					"redis": {
						Type: project.ResourceTypeDbRedis,
						Name: "redis",
					},
					"mongodb": {
						Type: project.ResourceTypeDbMongo,
						Name: "mongodb",
					},
					"postgres": {
						Type: project.ResourceTypeDbPostgres,
						Name: "postgres",
					},
					"py": {
						Type: project.ResourceTypeHostContainerApp,
						Name: "py",
						Uses: []string{"postgres", "mongodb", "redis"},
						Props: project.ContainerAppProps{
							Port: 80,
						},
					},
					"js": {
						Type: project.ResourceTypeHostContainerApp,
						Name: "js",
						Uses: []string{"py"},
						Props: project.ContainerAppProps{
							Port: 80,
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &Initializer{
				console: input.NewConsole(
					false,
					false,
					input.Writers{Output: os.Stdout},
					input.ConsoleHandles{
						Stderr: os.Stderr,
						Stdin:  strings.NewReader(strings.Join(tt.interactions, "\n") + "\n"),
						Stdout: os.Stdout,
					},
					nil,
					nil),
			}

			dir := t.TempDir()

			prjName := dir
			tt.want.Name = filepath.Base(prjName)
			tt.want.Metadata = &project.ProjectMetadata{
				Template: fmt.Sprintf("%s@%s", InitGenTemplateId, internal.VersionInfo().Version),
			}

			if tt.want.Resources == nil {
				tt.want.Resources = map[string]*project.ResourceConfig{}
			}

			for k, svc := range tt.want.Services {
				svc.Name = k
			}

			// Convert relative to absolute paths
			for idx, svc := range tt.detect.Services {
				tt.detect.Services[idx].Path = filepath.Join(dir, svc.Path)
				if tt.detect.Services[idx].Docker != nil {
					tt.detect.Services[idx].Docker.Path = filepath.Join(dir, svc.Path, svc.Docker.Path)
				}
			}

			spec, err := i.prjConfigFromDetect(
				context.Background(),
				dir,
				tt.detect,
				true)

			// Print extra newline to avoid mangling `go test -v` final test result output while waiting for final stdin,
			// which may result in incorrect `gotestsum` reporting
			fmt.Println()

			require.NoError(t, err)
			require.Equal(t, tt.want, spec)
		})
	}
}

const postgresPropertiesOriginalContent = `spring.datasource.url=jdbc:postgresql://localhost:5432/dbname?sslmode=require
spring.datasource.username=admin
spring.datasource.password=secret
`

var postgresPropertiesUpdatedContent = `spring.datasource.url=` + placeholderPostgresJdbcUrl + `
spring.datasource.username=` + internal.ToEnvPlaceHolder(internal.EnvNamePostgresUsername) + `
spring.datasource.password=` + internal.ToEnvPlaceHolder(internal.EnvNamePostgresPassword) + `
`

var postgresPropertiesOriginalMap = []property{
	{"spring.datasource.url", "jdbc:postgresql://localhost:5432/dbname?sslmode=require"},
	{"spring.datasource.username", "admin"},
	{"spring.datasource.password", "secret"},
}

func TestAddPostgresqlConnectionProperties(t *testing.T) {
	tests := []struct {
		name                                    string
		inputApplicationPropertiesContent       string
		inputApplicationAzurePropertiesContent  string
		outputApplicationPropertiesContent      string
		outputApplicationAzurePropertiesContent string
	}{
		{
			name:                                    "no content",
			inputApplicationPropertiesContent:       "",
			inputApplicationAzurePropertiesContent:  "",
			outputApplicationPropertiesContent:      "spring.profiles.active=" + azureProfileName + "\n",
			outputApplicationAzurePropertiesContent: postgresPropertiesUpdatedContent,
		},
		{
			name:                                    "override original content",
			inputApplicationPropertiesContent:       "spring.profiles.active=" + azureProfileName,
			inputApplicationAzurePropertiesContent:  postgresPropertiesOriginalContent,
			outputApplicationPropertiesContent:      "spring.profiles.active=" + azureProfileName + "\n",
			outputApplicationAzurePropertiesContent: postgresPropertiesUpdatedContent,
		},
		{
			name:                                   "append original content",
			inputApplicationPropertiesContent:      "aaa=xxx\n" + "spring.profiles.active=production , cloud,,",
			inputApplicationAzurePropertiesContent: "bbb=yyy\n" + postgresPropertiesOriginalContent,
			outputApplicationPropertiesContent: "aaa=xxx\n" + "spring.profiles.active=production,cloud," +
				azureProfileName + "\n",
			outputApplicationAzurePropertiesContent: "bbb=yyy\n" + postgresPropertiesUpdatedContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			applicationPropertiesPath := filepath.Join(tempDir, applicationPropertiesRelativePath)
			createFileIfContentIsNotEmpty(t, applicationPropertiesPath, tt.inputApplicationPropertiesContent)
			applicationAzurePropertiesPath := filepath.Join(tempDir, applicationAzurePropertiesRelativePath)
			createFileIfContentIsNotEmpty(t, applicationAzurePropertiesPath, tt.inputApplicationAzurePropertiesContent)
			err := addPostgresqlConnectionProperties(tempDir)
			assert.NoError(t, err)
			assertFileContent(t, applicationPropertiesPath, tt.outputApplicationPropertiesContent)
			assertFileContent(t, applicationAzurePropertiesPath, tt.outputApplicationAzurePropertiesContent)
		})
	}
}

func createFileIfContentIsNotEmpty(t *testing.T, path string, content string) {
	if content == "" {
		return
	}

	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err := os.MkdirAll(dir, os.ModePerm)
		assert.NoError(t, err)
	}
	file, err := os.Create(path)
	assert.NoError(t, err)
	defer file.Close()
	err = os.WriteFile(path, []byte(content), 0600)
	assert.NoError(t, err)
}

func assertFileContent(t *testing.T, path string, content string) {
	actualContent, err := os.ReadFile(path)
	assert.NoError(t, err)
	assert.Equal(t, content, string(actualContent))
}

func TestReadProperties(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test.properties")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.Write([]byte(postgresPropertiesOriginalContent)); err != nil {
		t.Fatal(err)
	}
	if err := tempFile.Close(); err != nil {
		t.Fatal(err)
	}

	properties, err := readProperties(tempFile.Name())
	if err != nil {
		t.Fatalf("readProperties() error = %v", err)
	}

	if !reflect.DeepEqual(properties, postgresPropertiesOriginalMap) {
		t.Errorf("readProperties() = %v, want %v", properties, postgresPropertiesOriginalMap)
	}
}

func TestWriteProperties(t *testing.T) {
	tempFile, err := os.CreateTemp("", "test.properties")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())

	if err := writeProperties(tempFile.Name(), postgresPropertiesOriginalMap); err != nil {
		t.Fatalf("writeProperties() error = %v", err)
	}

	content, err := os.ReadFile(tempFile.Name())
	if err != nil {
		t.Fatal(err)
	}

	if string(content) != postgresPropertiesOriginalContent {
		t.Errorf("writeProperties() = %v, want %v", string(content), postgresPropertiesOriginalContent)
	}
}
