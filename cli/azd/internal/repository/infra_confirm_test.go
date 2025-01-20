package repository

import (
	"context"
	"fmt"
	"github.com/azure/azure-dev/cli/azd/internal"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/assert"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/stretchr/testify/require"
)

func TestInitializer_infraSpecFromDetect(t *testing.T) {
	dbPostgres := &scaffold.DatabasePostgres{
		DatabaseName: "myappdb",
		AuthType:     "userAssignedManagedIdentity",
	}
	envsForPostgres, _ := scaffold.GetServiceBindingEnvsForPostgres(*dbPostgres)
	scaffoldStorageAccount := scaffold.AzureDepStorageAccount{
		ContainerNames: []string{"container1"},
		AuthType:       internal.AuthTypeConnectionString,
	}
	envsForStorage, _ := scaffold.GetServiceBindingEnvsForStorageAccount(scaffoldStorageAccount)
	envsForMongo := scaffold.GetServiceBindingEnvsForMongo()
	scaffoldServiceBus := scaffold.AzureDepServiceBus{
		Queues:   []string{"queue1"},
		IsJms:    true,
		AuthType: internal.AuthTypeConnectionString,
	}
	envsForServiceBus, _ := scaffold.GetServiceBindingEnvsForServiceBus(scaffoldServiceBus)
	scaffoldEventHubs := scaffold.AzureDepEventHubs{
		EventHubNames: []string{"eventhub1"},
		AuthType:      internal.AuthTypeConnectionString,
		UseKafka:      true,
	}
	envsForEventHubs, _ := scaffold.GetServiceBindingEnvsForEventHubs(scaffoldEventHubs)
	envsForCosmos := scaffold.GetServiceBindingEnvsForCosmos()
	scaffoldMysql := scaffold.DatabaseMySql{
		DatabaseName: "mysql-db",
		AuthType:     internal.AuthTypePassword,
	}
	envsForMysql, _ := scaffold.GetServiceBindingEnvsForMysql(scaffoldMysql)
	envsForCosmosMongo := scaffold.GetServiceBindingEnvsForMongo()
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
						Port:    8080,
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
			name: "api with storage",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.Java,
						Path:     "java",
						AzureDeps: []appdetect.AzureDep{
							appdetect.AzureDepStorageAccount{
								ContainerNamePropertyMap: map[string]string{
									"spring.cloud.azure.container": "container1",
								},
							},
						},
					},
				},
				AzureDeps: map[string]Pair{
					"storage": {
						first: appdetect.AzureDepStorageAccount{
							ContainerNamePropertyMap: map[string]string{
								"spring.cloud.azure.container": "container1",
							},
						},
						second: EntryKindDetected,
					},
				},
			},
			interactions: []string{
				"Connection string",
			},
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name:                "java",
						Port:                8080,
						Backend:             &scaffold.Backend{},
						AzureStorageAccount: &scaffoldStorageAccount,
						Envs:                envsForStorage,
					},
				},
				AzureStorageAccount: &scaffoldStorageAccount,
			},
		},
		{
			name: "api with mongo",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.Java,
						Path:     "java",
						DatabaseDeps: []appdetect.DatabaseDep{
							appdetect.DbMongo,
						},
					},
				},
				Databases: map[appdetect.DatabaseDep]EntryKind{
					appdetect.DbMongo: EntryKindDetected,
				},
			},
			interactions: []string{
				"mongodb-name",
			},
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name:    "java",
						Port:    8080,
						Backend: &scaffold.Backend{},
						DbCosmosMongo: &scaffold.DatabaseCosmosMongo{
							DatabaseName: "mongodb-name",
						},
						Envs: envsForMongo,
					},
				},
				DbCosmosMongo: &scaffold.DatabaseCosmosMongo{
					DatabaseName: "mongodb-name",
				},
			},
		},
		{
			name: "api with service bus",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.Java,
						Path:     "java",
						AzureDeps: []appdetect.AzureDep{
							appdetect.AzureDepServiceBus{
								Queues: []string{"queue1"},
								IsJms:  true,
							},
						},
					},
				},
				AzureDeps: map[string]Pair{
					"storage": {
						first: appdetect.AzureDepServiceBus{
							Queues: []string{"queue1"},
							IsJms:  true,
						},
						second: EntryKindDetected,
					},
				},
			},
			interactions: []string{
				"Connection string",
			},
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name:            "java",
						Port:            8080,
						Backend:         &scaffold.Backend{},
						AzureServiceBus: &scaffoldServiceBus,
						Envs:            envsForServiceBus,
					},
				},
				AzureServiceBus: &scaffoldServiceBus,
			},
		},
		{
			name: "api with event hubs",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.Java,
						Path:     "java",
						AzureDeps: []appdetect.AzureDep{
							appdetect.AzureDepEventHubs{
								EventHubsNamePropertyMap: map[string]string{
									"spring.cloud.azure.kafka": "eventhub1",
								},
								DependencyTypes: []appdetect.DependencyType{appdetect.SpringKafka},
							},
						},
					},
				},
				AzureDeps: map[string]Pair{
					"eventhubs": {
						first: appdetect.AzureDepEventHubs{
							EventHubsNamePropertyMap: map[string]string{
								"spring.cloud.azure.kafka": "eventhub1",
							},
							DependencyTypes: []appdetect.DependencyType{appdetect.SpringKafka},
						},
						second: EntryKindDetected,
					},
				},
			},
			interactions: []string{
				"Connection string",
			},
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name:           "java",
						Port:           8080,
						Backend:        &scaffold.Backend{},
						AzureEventHubs: &scaffoldEventHubs,
						Envs:           envsForEventHubs,
					},
				},
				AzureEventHubs: &scaffoldEventHubs,
			},
		},
		{
			name: "api with cosmos db",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.Java,
						Path:     "java",
						DatabaseDeps: []appdetect.DatabaseDep{
							appdetect.DbCosmos,
						},
					},
				},
				Databases: map[appdetect.DatabaseDep]EntryKind{
					appdetect.DbCosmos: EntryKindDetected,
				},
			},
			interactions: []string{
				"cosmos-db-name",
			},
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name:    "java",
						Port:    8080,
						Backend: &scaffold.Backend{},
						DbCosmos: &scaffold.DatabaseCosmosAccount{
							DatabaseName: "cosmos-db-name",
						},
						Envs: envsForCosmos,
					},
				},
				DbCosmos: &scaffold.DatabaseCosmosAccount{
					DatabaseName: "cosmos-db-name",
				},
			},
		},
		{
			name: "api with mysql",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.Java,
						Path:     "java",
						DatabaseDeps: []appdetect.DatabaseDep{
							appdetect.DbMySql,
						},
					},
				},
				Databases: map[appdetect.DatabaseDep]EntryKind{
					appdetect.DbMySql: EntryKindDetected,
				},
			},
			interactions: []string{
				// prompt for dbname
				"mysql-db",
				"Username and password",
			},
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name:    "java",
						Port:    8080,
						Backend: &scaffold.Backend{},
						DbMySql: &scaffoldMysql,
						Envs:    envsForMysql,
					},
				},
				DbMySql: &scaffoldMysql,
			},
		},
		{
			name: "api with cosmos db mongo",
			detect: detectConfirm{
				Services: []appdetect.Project{
					{
						Language: appdetect.Java,
						Path:     "java",
						DatabaseDeps: []appdetect.DatabaseDep{
							appdetect.DbMongo,
						},
					},
				},
				Databases: map[appdetect.DatabaseDep]EntryKind{
					appdetect.DbMongo: EntryKindDetected,
				},
			},
			interactions: []string{
				"cosmos-db-mongo-name",
			},
			want: scaffold.InfraSpec{
				Services: []scaffold.ServiceSpec{
					{
						Name:    "java",
						Port:    8080,
						Backend: &scaffold.Backend{},
						DbCosmosMongo: &scaffold.DatabaseCosmosMongo{
							DatabaseName: "cosmos-db-mongo-name",
						},
						Envs: envsForCosmosMongo,
					},
				},
				DbCosmosMongo: &scaffold.DatabaseCosmosMongo{
					DatabaseName: "cosmos-db-mongo-name",
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
				"myappdb",                        // fill in db name
				"User assigned managed identity", // confirm db authentication
			},
			want: scaffold.InfraSpec{
				DbPostgres: &scaffold.DatabasePostgres{
					DatabaseName: "myappdb",
					AuthType:     "userAssignedManagedIdentity",
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
						DbPostgres: dbPostgres,
						Envs:       envsForPostgres,
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

			dir := t.TempDir()
			tt.detect.root = dir

			spec, err := i.infraSpecFromDetect(context.Background(), &tt.detect)

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
