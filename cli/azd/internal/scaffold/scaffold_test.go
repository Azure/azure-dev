// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

	functionHelperCollisionSpec := InfraSpec{
		StorageAccount: &StorageAccount{Implicit: true},
	}
	for _, name := range []string{
		"api", "api-plan", "api-function-storage", "api-insights", "api-insights-metrics-publisher",
	} {
		functionHelperCollisionSpec.Services = append(functionHelperCollisionSpec.Services, ServiceSpec{
			Name:            name,
			Host:            FunctionAppKind,
			Runtime:         &RuntimeInfo{Type: "python", Version: "3.12"},
			FunctionStorage: &FunctionStorage{},
		})
	}

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
			"Function Apps with helper symbol collisions",
			functionHelperCollisionSpec,
		},
		{
			"Function App with implicit storage",
			InfraSpec{
				StorageAccount: &StorageAccount{Implicit: true},
				Services: []ServiceSpec{{
					Name: "api",
					Host: FunctionAppKind,
					Runtime: &RuntimeInfo{
						Type: "python", Version: "3.12",
					},
					FunctionStorage: &FunctionStorage{},
					Env:             map[string]string{"MY-SETTING": "'v'", "Logging.Level": "'Info'"},
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
			"Function Apps with existing and implicit storage",
			InfraSpec{
				StorageAccount: &StorageAccount{Implicit: true},
				Existing: []ExistingResource{{
					Name:             "existingStorage",
					ResourceType:     "Microsoft.Storage/storageAccounts",
					ApiVersion:       "2023-05-01",
					ResourceIdEnvVar: "AZURE_RESOURCE_STORAGE_ID",
				}},
				Services: []ServiceSpec{
					{
						Name: "api", Host: FunctionAppKind,
						Runtime:         &RuntimeInfo{Type: "python", Version: "3.12"},
						FunctionStorage: &FunctionStorage{ExistingName: "existingStorage"},
					},
					{
						Name: "worker", Host: FunctionAppKind,
						Runtime:         &RuntimeInfo{Type: "node", Version: "22"},
						FunctionStorage: &FunctionStorage{},
					},
				},
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
			if tt.name == "Function Apps with helper symbol collisions" {
				bicep, err := os.ReadFile(filepath.Join(dir, "resources.bicep"))
				require.NoError(t, err)
				source := string(bicep)
				for _, service := range tt.spec.Services {
					symbol := BicepName(service.Name)
					for _, helper := range []string{"Plan", "Storage", "InsightsMetricsPublisher"} {
						assert.Contains(t, source, "_function"+helper+"_"+symbol+" '")
					}
					assert.Contains(t, source, "module "+symbol+" 'br/public:avm/res/web/site:0.24.0'")
					assert.Contains(t, source, "name: '"+symbol+"-plan'")
					assert.Contains(t, source, "name: '"+symbol+"-function-storage'")
					assert.Contains(t, source, "name: 'function-app-"+symbol+"'")
					assert.Contains(t, source, "name: '${abbrs.webSitesFunctions}"+service.Name+"-${resourceToken}'")
					assert.Contains(t, source,
						"output AZURE_RESOURCE_"+AlphaSnakeUpper(service.Name)+"_ID string = "+symbol+".outputs.resourceId")
				}
			}
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
					assert.Contains(t, string(bicep),
						"keyVaultAccessIdentityResourceId: "+BicepName(service.Name)+"Identity.outputs.resourceId")
					if tt.name == "Function App with implicit storage" {
						assert.Contains(t, string(bicep), "'MY-SETTING': 'v'")
						assert.Contains(t, string(bicep), "'Logging.Level': 'Info'")
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
					Resources []struct {
						Name      string   `json:"name"`
						DependsOn []string `json:"dependsOn"`
					} `json:"resources"`
				}
				require.NoError(t, json.Unmarshal([]byte(resourceTemplate.Compiled), &compiled))
				dependencies := make(map[string][]string, len(compiled.Resources))
				for _, resource := range compiled.Resources {
					dependencies[resource.Name] = resource.DependsOn
				}
				deployment := func(name string) string {
					return "[resourceId('Microsoft.Resources/deployments', '" + name + "')]"
				}
				assert.Contains(t, dependencies["api-function-storage"], deployment("storageAccount"))
				assert.Contains(t, dependencies["api-function-storage"], deployment("apiidentity"))
				assert.Contains(t, dependencies["function-app-api"], deployment("api-function-storage"))
				assert.Contains(t, dependencies["function-app-api"], deployment("monitoring"))
				assert.Contains(t, dependencies["function-metrics-api"], deployment("function-app-api"))
				assert.Contains(t, dependencies["function-metrics-api"], deployment("monitoring"))
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

func TestServiceIdentityDeploymentName(t *testing.T) {
	tmpl, err := Load()
	require.NoError(t, err)

	identityDeployment := regexp.MustCompile(
		`(?m)^module (\w+)Identity '[^']+' = \{\n  name: '([^']+)'`)
	for _, host := range []HostKind{FunctionAppKind, AppServiceKind, ContainerAppKind} {
		for _, length := range []int{3, 41, 56, 57, 63} {
			t.Run(string(host)+"/"+strconv.Itoa(length), func(t *testing.T) {
				name := strings.Repeat("a", length)
				spec := InfraSpec{
					StorageAccount: &StorageAccount{Implicit: true},
					Services: []ServiceSpec{{
						Name:            name,
						Host:            host,
						Runtime:         &RuntimeInfo{Type: "python", Version: "3.12"},
						FunctionStorage: &FunctionStorage{},
					}},
				}
				files, err := ExecInfraFs(tmpl, spec)
				require.NoError(t, err)
				source, err := fs.ReadFile(files, "resources.bicep")
				require.NoError(t, err)

				normalizedSource := strings.ReplaceAll(string(source), "\r\n", "\n")
				matches := identityDeployment.FindAllStringSubmatch(normalizedSource, -1)
				require.Len(t, matches, 1)
				assert.Equal(t, BicepName(name), matches[0][1])
				deploymentName := matches[0][2]
				if host == FunctionAppKind {
					assert.Equal(t, FunctionAppName(BicepName(name))+"identity", deploymentName)
					assert.LessOrEqual(t, len(deploymentName), 64)
				} else {
					assert.Equal(t, BicepName(name)+"identity", deploymentName)
				}
				assert.Contains(t, string(source),
					"name: '${abbrs.managedIdentityUserAssignedIdentities}"+BicepName(name)+"-${resourceToken}'")
			})
		}
	}
}

func TestExistingResourceRoleAssignmentDeploymentName(t *testing.T) {
	tmpl, err := Load()
	require.NoError(t, err)

	roleDeployment := regexp.MustCompile(
		`(?m)^module (\w+) 'modules/role-assignment.bicep' = \{\n  name: '([^']+)'\n  scope:`)
	for _, host := range []HostKind{FunctionAppKind, AppServiceKind, ContainerAppKind} {
		for _, length := range []int{3, 41, 63} {
			t.Run(string(host)+"/"+strconv.Itoa(length), func(t *testing.T) {
				spec := InfraSpec{}
				for _, name := range []string{"existingStorage", "existingStorageOther"} {
					spec.Existing = append(spec.Existing, ExistingResource{
						Name:             name,
						ResourceType:     "Microsoft.Storage/storageAccounts",
						ApiVersion:       "2023-05-01",
						ResourceIdEnvVar: AlphaSnakeUpper(name) + "_ID",
						RoleAssignments: []RoleAssignment{
							{Name: "Contributor", RoleDefinitionId: "ba92f5b4-2d11-453d-a403-e96b0029c9fe"},
							{Name: "Reader", RoleDefinitionId: "2a2b9908-6ea1-4ae2-8e65-a410df84e7d1"},
						},
					})
				}
				for _, name := range []string{strings.Repeat("a", length), strings.Repeat("a", length-1) + "b"} {
					spec.Services = append(spec.Services, ServiceSpec{
						Name:            name,
						Host:            host,
						Runtime:         &RuntimeInfo{Type: "python", Version: "3.12"},
						FunctionStorage: &FunctionStorage{ExistingName: "existingStorage"},
						Existing:        []*ExistingResource{&spec.Existing[0], &spec.Existing[1]},
					})
				}
				files, err := ExecInfraFs(tmpl, spec)
				require.NoError(t, err)
				source, err := fs.ReadFile(files, "resources.bicep")
				require.NoError(t, err)
				normalizedSource := strings.ReplaceAll(string(source), "\r\n", "\n")
				matches := roleDeployment.FindAllStringSubmatch(normalizedSource, -1)
				require.Len(t, matches, 8)

				deployments := make(map[string]string, len(matches))
				names := make(map[string]bool, len(matches))
				for _, match := range matches {
					deployments[match[1]] = match[2]
					assert.False(t, names[match[2]], "duplicate deployment name: %s", match[2])
					names[match[2]] = true
				}
				for _, service := range spec.Services {
					for _, existing := range service.Existing {
						for _, role := range existing.RoleAssignments {
							symbol := BicepName(service.Name) + "_" + existing.Name + "_" + role.Name
							deploymentName, ok := deployments[symbol]
							require.True(t, ok, "missing role assignment module: %s", symbol)
							if host == FunctionAppKind {
								assert.Equal(t, FunctionAppName(symbol), deploymentName)
								assert.LessOrEqual(t, len(deploymentName), 64)
							} else {
								assert.Equal(t, symbol, deploymentName)
							}
						}
					}
				}
			})
		}
	}
}

func TestFunctionAppInsightsRoleAssignment(t *testing.T) {
	tmpl, err := Load()
	require.NoError(t, err)

	for _, existing := range []bool{false, true} {
		name := "without existing resources"
		if existing {
			name = "with existing resources"
		}
		t.Run(name, func(t *testing.T) {
			spec := InfraSpec{StorageAccount: &StorageAccount{Implicit: true}}
			if existing {
				spec.Existing = []ExistingResource{{
					Name:             "existingStorage",
					ResourceType:     "Microsoft.Storage/storageAccounts",
					ApiVersion:       "2023-05-01",
					ResourceIdEnvVar: "AZURE_RESOURCE_STORAGE_ID",
				}}
			}
			for _, serviceName := range []string{"api", "worker"} {
				spec.Services = append(spec.Services, ServiceSpec{
					Name:            serviceName,
					Host:            FunctionAppKind,
					Runtime:         &RuntimeInfo{Type: "python", Version: "3.12"},
					FunctionStorage: &FunctionStorage{},
				})
			}

			files, err := ExecInfraFs(tmpl, spec)
			require.NoError(t, err)
			source, err := fs.ReadFile(files, "resources.bicep")
			require.NoError(t, err)
			normalizedSource := strings.ReplaceAll(string(source), "\r\n", "\n")
			for _, service := range spec.Services {
				symbol := BicepName(service.Name)
				assert.Contains(t, normalizedSource,
					"module _functionInsightsMetricsPublisher_"+symbol+" 'modules/role-assignment.bicep' = {\n"+
						"  name: 'function-metrics-"+FunctionAppName(symbol)+"'\n"+
						"  params: {\n"+
						"    resourceId: monitoring.outputs.applicationInsightsResourceId\n"+
						"    roleDefinitionId: '3913510d-42f4-4e42-8a64-420c390055eb'\n"+
						"    principalId: "+symbol+".outputs.systemAssignedMIPrincipalId!\n"+
						"    principalType: 'ServicePrincipal'\n"+
						"  }\n}")
			}
			module, err := fs.ReadFile(files, "modules/role-assignment.bicep")
			require.NoError(t, err)
			assert.Contains(t, string(module), "name: guid(resourceId, principalId, fullRoleDefinitionId)")
			assert.Contains(t, string(module), "loadTextContent('./role-assignment.json')")
			roleTemplate, err := fs.ReadFile(files, "modules/role-assignment.json")
			require.NoError(t, err)
			assert.Contains(t, string(roleTemplate), `"principalId": "[parameters('principalId')]"`)
		})
	}
}
