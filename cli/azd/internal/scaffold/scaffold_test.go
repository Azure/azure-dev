// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/pkg/exec"
	"github.com/azure/azure-dev/pkg/tools/bicep"
	"github.com/azure/azure-dev/test/mocks/mockinput"
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

			lintErrs := strings.Split(res.LintErr, "\n")
			for _, lintErr := range lintErrs {
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
