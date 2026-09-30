// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
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
						Host: "containerapp",
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
						Host:     "containerapp",
					},
				},
			},
		},
		{
			"App Service Python",
			InfraSpec{
				Services: []ServiceSpec{
					{
						Name: "py",
						Port: 3100,
						Host: "appservice",
						Runtime: &RuntimeInfo{
							Type:    "python",
							Version: "3.11",
						},
					},
				},
			},
		},
		{
			"App Service Node",
			InfraSpec{
				Services: []ServiceSpec{
					{
						Name: "node",
						Port: 3100,
						Host: "appservice",
						Runtime: &RuntimeInfo{
							Type:    "node",
							Version: "22-lts",
						},
					},
				},
			},
		},
		{
			"Function App with implicit storage",
			InfraSpec{
				StorageAccount: &StorageAccount{},
				Services: []ServiceSpec{{
					Name: "api",
					Host: FunctionAppKind,
					Runtime: &RuntimeInfo{
						Type: "python", Version: "3.12",
					},
					FunctionStorage: &FunctionStorage{},
				}},
			},
		},
		{
			"Function App with managed storage",
			InfraSpec{
				StorageAccount: &StorageAccount{},
				Services: []ServiceSpec{{
					Name: "api",
					Host: FunctionAppKind,
					Runtime: &RuntimeInfo{
						Type: "node", Version: "22",
					},
					FunctionStorage: &FunctionStorage{},
					StorageAccount:  &StorageReference{},
				}},
			},
		},
		{
			"Function App with existing storage",
			InfraSpec{
				Existing: []ExistingResource{{
					Name:             "existingStorage",
					ResourceType:     "Microsoft.Storage/storageAccounts",
					ApiVersion:       "2023-05-01",
					ResourceIdEnvVar: "AZURE_RESOURCE_STORAGE_ID",
				}},
				Services: []ServiceSpec{{
					Name: "api",
					Host: FunctionAppKind,
					Runtime: &RuntimeInfo{
						Type: "dotnet-isolated", Version: "8.0",
					},
					FunctionStorage: &FunctionStorage{ExistingName: "existingStorage"},
				}},
			},
		},
		{
			"Go Function App and mixed hosts with dependencies",
			InfraSpec{
				StorageAccount: &StorageAccount{},
				DbCosmos:       &DatabaseCosmos{DatabaseName: "appdb"},
				DbRedis:        &DatabaseRedis{},
				KeyVault:       &KeyVault{},
				ServiceBus:     &ServiceBus{},
				EventHubs:      &EventHubs{},
				Services: []ServiceSpec{
					{
						Name: "goapi", Host: FunctionAppKind,
						Runtime:         &RuntimeInfo{Type: "go", Version: "1.0"},
						FunctionStorage: &FunctionStorage{},
						DbCosmos:        &DatabaseReference{DatabaseName: "appdb"},
						DbRedis:         &DatabaseReference{DatabaseName: "redis"},
						ServiceBus:      &ServiceBus{},
						EventHubs:       &EventHubs{},
					},
					{
						Name: "pyapi", Host: FunctionAppKind,
						Runtime:         &RuntimeInfo{Type: "python", Version: "3.12"},
						FunctionStorage: &FunctionStorage{},
						StorageAccount:  &StorageReference{},
					},
					{Name: "web", Host: AppServiceKind, Port: 3100,
						Runtime: &RuntimeInfo{Type: "node", Version: "22-lts"}},
					{Name: "worker", Host: ContainerAppKind, Port: 3100},
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
						Host: "containerapp",
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
						Host: "containerapp",
					},
				},
			},
		},
		{
			"All",
			InfraSpec{
				AiFoundryProject: &AiFoundrySpec{
					Name: "project",
					Models: []AiFoundryModel{
						{
							AIModelModel: AIModelModel{
								Name:    "model",
								Version: "1.0",
							},
							Format: "OpenAI",
							Sku: AiFoundryModelSku{
								Name:      "S0",
								UsageName: "S0",
								Capacity:  1,
							},
						},
					},
				},
				DbPostgres: &DatabasePostgres{
					DatabaseName: "appdb",
				},
				DbMySql: &DatabaseMysql{
					DatabaseName: "mysqldb",
				},
				DbCosmosMongo: &DatabaseCosmosMongo{
					DatabaseName: "appdb",
				},
				DbCosmos: &DatabaseCosmos{
					DatabaseName: "cosmos",
				},
				DbRedis:        &DatabaseRedis{},
				ServiceBus:     &ServiceBus{},
				EventHubs:      &EventHubs{},
				StorageAccount: &StorageAccount{},
				KeyVault:       &KeyVault{},
				AISearch:       &AISearch{},
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
						DbCosmosMongo: &DatabaseReference{
							DatabaseName: "appdb",
						},
						DbRedis: &DatabaseReference{
							DatabaseName: "redis",
						},
						DbPostgres: &DatabaseReference{
							DatabaseName: "appdb",
						},
						DbCosmos: &DatabaseReference{
							DatabaseName: "cosmos",
						},
						DbMySql: &DatabaseReference{
							DatabaseName: "mysqldb",
						},
						ServiceBus:       &ServiceBus{},
						EventHubs:        &EventHubs{},
						StorageAccount:   &StorageReference{},
						KeyVault:         &KeyVaultReference{},
						AISearch:         &AISearchReference{},
						AiFoundryProject: &AiFoundrySpec{},
						Host:             "containerapp",
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
						Host: "containerapp",
					},
					{
						Name: "app",
						Port: 3000,
						Host: "appservice",
						Runtime: &RuntimeInfo{
							Type:    "python",
							Version: "3.11",
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
				},
				KeyVault: &KeyVault{},
				Services: []ServiceSpec{
					{
						Name: "api",
						Port: 3100,
						DbPostgres: &DatabaseReference{
							DatabaseName: "appdb",
						},
						Host: "containerapp",
					},
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
			if tt.name == "Go Function App and mixed hosts with dependencies" {
				bicep, err := os.ReadFile(filepath.Join(dir, "resources.bicep"))
				require.NoError(t, err)
				assert.Contains(t, string(bicep), "http20Enabled: false")
				assert.Contains(t, string(bicep), "name: 'go'")
				assert.Contains(t, string(bicep), "AZURE_SERVICE_BUS_NAME: serviceBusNamespace.outputs.name")
				assert.Contains(t, string(bicep), "REDIS_HOST: redis.outputs.hostName")
			}
			for _, service := range tt.spec.Services {
				if service.Host == FunctionAppKind {
					bicep, err := os.ReadFile(filepath.Join(dir, "resources.bicep"))
					require.NoError(t, err)
					assert.NotContains(t, string(bicep), "FUNCTIONS_WORKER_RUNTIME")
					assert.Regexp(t,
						`siteConfig:\s*\{\s*alwaysOn: false\s*`+
							`cors:\s*\{\s*allowedOrigins:\s*\[\s*'https://portal.azure.com'`,
						string(bicep))
					assert.Contains(t, string(bicep), "type: 'UserAssignedIdentity'")
					assert.Contains(t, string(bicep), "AzureWebJobsStorage__clientId:")
					if tt.name == "Function App with implicit storage" {
						assert.Regexp(t, `networkAcls:\s*\{\s*defaultAction: 'Allow'\s*\}`, string(bicep))
					}
					module, err := os.ReadFile(filepath.Join(dir, "modules", "function-storage.bicep"))
					require.NoError(t, err)
					for _, role := range []string{"blobOwner", "queueContributor", "tableContributor"} {
						assert.Contains(t, string(module), "resource "+role+" ")
					}
					break
				}
			}
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

			ctx := t.Context()
			cli := bicep.NewCli(mockinput.NewMockConsole(), exec.NewCommandRunner(nil))

			res, err := cli.Build(ctx, filepath.Join(dir, "main.bicep"))
			require.NoError(t, err)

			if tt.name == "Function App with implicit storage" || tt.name == "Function App with managed storage" {
				resourceTemplate, err := cli.Build(ctx, filepath.Join(dir, "resources.bicep"))
				require.NoError(t, err)
				var compiled struct {
					Resources map[string]struct {
						DependsOn []string `json:"dependsOn"`
					} `json:"resources"`
				}
				require.NoError(t, json.Unmarshal([]byte(resourceTemplate.Compiled), &compiled))
				assert.Contains(t, compiled.Resources["apiFunctionStorage"].DependsOn, "storageAccount")
				assert.Contains(t, compiled.Resources["apiFunctionStorage"].DependsOn, "apiIdentity")
				assert.Contains(t, compiled.Resources["api"].DependsOn, "apiFunctionStorage")
				assert.Contains(t, compiled.Resources["apiInsightsMetricsPublisher"].DependsOn, "api")
				assert.Contains(t, compiled.Resources["api"].DependsOn, "monitoring")
			}

			lintErrs := strings.SplitSeq(res.LintErr, "\n")
			for lintErr := range lintErrs {
				if lintErr == "" {
					continue
				}

				// suppress these errors
				if strings.Contains(lintErr, "no-unused-params: Parameter \"principalId\" ") ||
					strings.Contains(lintErr, "no-unused-params: Parameter \"principalType\" ") {
					// we always set principalId and principalType regardless of whether they are used
					// in the current implementation
					continue
				}

				assert.Failf(t, "found bicep lint error", "lint: %s", lintErr)
			}
		})
	}
}
