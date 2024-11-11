package repository

import (
	"context"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/assert"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/stretchr/testify/require"
)

func TestInitializer_infraSpecFromDetect(t *testing.T) {
	tests := []struct {
		name         string
		detect       detectConfirm
		interactions []string
		want         scaffold.InfraSpec
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
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name:    "dotnet",
						Port:    80,
						Backend: &scaffold.Backend{},
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
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name:     "js",
						Port:     80,
						Frontend: &scaffold.Frontend{},
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
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name:    "dotnet",
						Port:    1234,
						Backend: &scaffold.Backend{},
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
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name: "py",
						Port: 80,
						Backend: &scaffold.Backend{
							Frontends: []scaffold.ServiceReference{
								{
									Name: "js",
								},
							},
						},
					},
					{
						Name: "js",
						Port: 80,
						Frontend: &scaffold.Frontend{
							Backends: []scaffold.ServiceReference{
								{
									Name: "py",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "api and web with db",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.Python,
						Path:     "py",
						DatabaseDeps: []appdetect.DatabaseDep{
							appdetect.DbPostgres,
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
				},
			},
			interactions: []string{
				"my app db",
				"n",
				"my$special$db",
				"n",
				"myappdb",                            // fill in db name
				"Use user assigned managed identity", // confirm db authentication
			},
			want: scaffold.InfraSpec{
				DbPostgres: &scaffold.DatabasePostgres{
					DatabaseName:             "myappdb",
					AuthUsingManagedIdentity: true,
				},
				Services: []scaffold.ServiceSpec{
					{
						Name: "py",
						Port: 80,
						Backend: &scaffold.Backend{
							Frontends: []scaffold.ServiceReference{
								{
									Name: "js",
								},
							},
						},
						DbPostgres: &scaffold.DatabaseReference{
							DatabaseName:             "myappdb",
							AuthUsingManagedIdentity: true,
						},
					},
					{
						Name: "js",
						Port: 80,
						Frontend: &scaffold.Frontend{
							Backends: []scaffold.ServiceReference{
								{
									Name: "py",
								},
							},
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

			spec, err := i.infraSpecFromDetect(context.Background(), tt.detect)

			// Print extra newline to avoid mangling `go test -v` final test result output while waiting for final stdin,
			// which may result in incorrect `gotestsum` reporting
			fmt.Println()

			require.NoError(t, err)
			require.Equal(t, tt.want, spec)
		})
	}
}

func TestDetectCosmosSqlDatabaseContainerInFile(t *testing.T) {
	tests := []struct {
		javaFileContent    string
		expectedContainers scaffold.CosmosSqlDatabaseContainer
	}{
		{
			javaFileContent: "",
			expectedContainers: scaffold.CosmosSqlDatabaseContainer{
				ContainerName:     "",
				PartitionKeyPaths: []string{},
			},
		},
		{
			javaFileContent: "@Container(containerName = \"users\")",
			expectedContainers: scaffold.CosmosSqlDatabaseContainer{
				ContainerName:     "users",
				PartitionKeyPaths: []string{},
			},
		},
		{
			javaFileContent: "" +
				"@Container(containerName = \"users\")\n" +
				"public class User {\n" +
				"    @Id\n    " +
				"private String id;\n" +
				"    private String firstName;\n" +
				"    @PartitionKey\n" +
				"    private String lastName;",
			expectedContainers: scaffold.CosmosSqlDatabaseContainer{
				ContainerName: "users",
				PartitionKeyPaths: []string{
					"lastName",
				},
			},
		},
		{
			javaFileContent: "" +
				"@Container(containerName = \"users\")\n" +
				"public class User {\n" +
				"    @Id\n    " +
				"private String id;\n" +
				"    private String firstName;\n" +
				"    @PartitionKey private String lastName;",
			expectedContainers: scaffold.CosmosSqlDatabaseContainer{
				ContainerName: "users",
				PartitionKeyPaths: []string{
					"lastName",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.javaFileContent, func(t *testing.T) {
			tempDir := t.TempDir()
			tempFile := filepath.Join(tempDir, "Example.java")
			file, err := os.Create(tempFile)
			assert.NoError(t, err)
			file.Close()

			err = os.WriteFile(tempFile, []byte(tt.javaFileContent), osutil.PermissionFile)
			assert.NoError(t, err)

			container, err := detectCosmosSqlDatabaseContainerInFile(tempFile)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedContainers, container)
		})
	}
}
