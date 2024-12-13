package repository

import (
	"context"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
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
				"Username and password",
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
					"mongo": {
						Type: project.ResourceTypeDbMongo,
						Name: "mongo",
						Props: project.MongoDBProps{
							DatabaseName: "mongodb",
						},
					},
					"postgresql": {
						Type: project.ResourceTypeDbPostgres,
						Name: "postgresql",
						Props: project.PostgresProps{
							AuthType:     internal.AuthTypePassword,
							DatabaseName: "postgres",
						},
					},
					"py": {
						Type: project.ResourceTypeHostContainerApp,
						Name: "py",
						Uses: []string{"postgresql", "mongo", "redis"},
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
				&tt.detect,
				&scaffold.InfraSpec{},
				true)

			// Print extra newline to avoid mangling `go test -v` final test result output while waiting for final stdin,
			// which may result in incorrect `gotestsum` reporting
			fmt.Println()

			require.NoError(t, err)
			require.Equal(t, tt.want, spec)
		})
	}
}
