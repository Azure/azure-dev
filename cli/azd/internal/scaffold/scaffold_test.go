package scaffold

import (
	"context"
	"github.com/azure/azure-dev/cli/azd/internal"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/otiai10/copy"
	"github.com/stretchr/testify/require"
)

// Verify that the scaffolded infrastructure is valid bicep and free of lint errors.
//
// To have generated files saved under ./testdata, set SCAFFOLD_SAVE=true.
func TestExecInfra(t *testing.T) {
	template, err := Load()
	require.NoError(t, err)

	tests := []struct {
		name string
		spec InfraSpec
	}{
		{
			"API only",
			InfraSpec{
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
					},
				},
			},
		},
		{
			"Web only",
			InfraSpec{
				Services: []ServiceSpec{
					{
						Name:     "web",
						Port:     3100,
						Frontend: &Frontend{},
					},
				},
			},
		},
		{
			"API and web",
			InfraSpec{
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
						Backend: &Backend{
							Frontends: []ServiceReference{
								{
									Name: "web",
								},
							},
						},
					},
					{
						Name: "web",
						Port: 3101,
						Frontend: &Frontend{
							Backends: []ServiceReference{
								{
									Name: "api",
								},
							},
						},
					},
				},
			},
		},
		{
			"All",
			InfraSpec{
				DbPostgres: &DatabasePostgres{
					DatabaseName: "appdb",
				},
				DbCosmosMongo: &DatabaseCosmosMongo{
					DatabaseName: "appdb",
				},
				DbRedis: &DatabaseRedis{},
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
						Backend: &Backend{
							Frontends: []ServiceReference{
								{
									Name: "web",
								},
							},
						},
						DbCosmosMongo: &DatabaseCosmosMongo{
							DatabaseName: "appdb",
						},
						DbRedis: &DatabaseRedis{},
						DbPostgres: &DatabasePostgres{
							DatabaseName: "appdb",
						},
					},
					{
						Name: "web",
						Port: 3101,
						Frontend: &Frontend{
							Backends: []ServiceReference{
								{
									Name: "api",
								},
							},
						},
					},
				},
			},
		},
		{
			"API with Postgres",
			InfraSpec{
				DbPostgres: &DatabasePostgres{
					DatabaseName: "appdb",
					DatabaseUser: "appuser",
				},
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
						DbPostgres: &DatabasePostgres{
							DatabaseName: "appdb",
						},
					},
				},
			},
		},
		{
			"API with MongoDB",
			InfraSpec{
				DbCosmosMongo: &DatabaseCosmosMongo{
					DatabaseName: "appdb",
				},
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
						DbCosmosMongo: &DatabaseCosmosMongo{
							DatabaseName: "appdb",
						},
					},
				},
			},
		},
		{
			"API with Redis",
			InfraSpec{
				DbRedis: &DatabaseRedis{},
				Services: []ServiceSpec{
					{
						Name:    "api",
						Port:    3100,
						DbRedis: &DatabaseRedis{},
					},
				},
			},
		},
		{
			"API with Storage Account",
			InfraSpec{
				AzureStorageAccount: &AzureDepStorageAccount{
					ContainerNames: []string{"container1"},
				},
				Services: []ServiceSpec{
					{
						Name:                "api",
						Port:                3100,
						AzureStorageAccount: &AzureDepStorageAccount{},
					},
				},
			},
		},
		{
			"API with Service Bus",
			InfraSpec{
				AzureServiceBus: &AzureDepServiceBus{
					Queues:   []string{"queue1"},
					AuthType: internal.AuthTypeUserAssignedManagedIdentity,
					IsJms:    true,
				},
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
						AzureServiceBus: &AzureDepServiceBus{
							Queues:   []string{"queue1"},
							AuthType: internal.AuthTypeUserAssignedManagedIdentity,
							IsJms:    true,
						},
					},
				},
			},
		},
		{
			"API with Event Hubs",
			InfraSpec{
				AzureEventHubs: &AzureDepEventHubs{
					EventHubNames:     []string{"eventhub1"},
					AuthType:          internal.AuthTypeUserAssignedManagedIdentity,
					UseKafka:          true,
					SpringBootVersion: "3.4.0",
				},
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
						AzureEventHubs: &AzureDepEventHubs{
							EventHubNames:     []string{"eventhub1"},
							AuthType:          internal.AuthTypeUserAssignedManagedIdentity,
							UseKafka:          true,
							SpringBootVersion: "3.4.0",
						},
					},
				},
			},
		},
		{
			"API with Cosmos DB",
			InfraSpec{
				DbCosmos: &DatabaseCosmosAccount{
					DatabaseName: "cosmos-db",
					Containers: []CosmosSqlDatabaseContainer{
						{
							ContainerName:     "container1",
							PartitionKeyPaths: []string{"/partitionKey"},
						},
					},
				},
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
						DbCosmos: &DatabaseCosmosAccount{
							DatabaseName: "cosmos-db",
							Containers: []CosmosSqlDatabaseContainer{
								{
									ContainerName:     "container1",
									PartitionKeyPaths: []string{"/partitionKey"},
								},
							},
						},
					},
				},
			},
		},
		{
			"API with MySQL password",
			InfraSpec{
				DbMySql: &DatabaseMySql{
					DatabaseName: "appdb",
					DatabaseUser: "appuser",
					AuthType:     internal.AuthTypePassword,
				},
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
						DbMySql: &DatabaseMySql{
							DatabaseName: "appdb",
							DatabaseUser: "appuser",
							AuthType:     internal.AuthTypePassword,
						},
					},
				},
			},
		},
		{
			"API with MySQL umi",
			InfraSpec{
				DbMySql: &DatabaseMySql{
					DatabaseName: "appdb",
					DatabaseUser: "appuser",
					AuthType:     internal.AuthTypeUserAssignedManagedIdentity,
				},
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
						DbMySql: &DatabaseMySql{
							DatabaseName: "appdb",
							DatabaseUser: "appuser",
							AuthType:     internal.AuthTypeUserAssignedManagedIdentity,
						},
					},
				},
			},
		},
		// with azd add, users could add only mongo resource
		{
			"Only Mongo",
			InfraSpec{
				DbCosmosMongo: &DatabaseCosmosMongo{},
			},
		},
		// with azd add, users could add only redis resource
		{
			"Only Redis",
			InfraSpec{
				DbRedis: &DatabaseRedis{},
			},
		},
		// with azd add, users could add only postgresql resource
		{
			"Only Postgres",
			InfraSpec{
				DbPostgres: &DatabasePostgres{
					AuthType: internal.AuthTypeUserAssignedManagedIdentity,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			err := ExecInfra(
				template,
				tt.spec,
				dir)
			require.NoError(t, err)

			if v := os.Getenv("SCAFFOLD_SAVE"); v != "" {
				dest := filepath.Join("testdata", strings.ReplaceAll(t.Name(), "/", "-"))
				err := os.MkdirAll(dest, 0700)
				require.NoError(t, err)

				err = copy.Copy(dir, dest)
				require.NoError(t, err)
			}

			if testing.Short() {
				return
			}

			ctx := context.Background()
			cli, err := bicep.NewCli(ctx, mockinput.NewMockConsole(), exec.NewCommandRunner(nil))
			require.NoError(t, err)

			res, err := cli.Build(ctx, filepath.Join(dir, "main.bicep"))
			require.NoError(t, err)
			require.Empty(t, res.LintErr, "lint errors occurred")
		})
	}
}
