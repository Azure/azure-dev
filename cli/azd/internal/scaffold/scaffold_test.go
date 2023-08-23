package scaffold

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

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
				Parameters: []Parameter{
					NewContainerAppServiceExistsParameter("api"),
				},
				Services: []ServiceSpec{
					ServiceSpec{
						Name: "api",
						Port: 3100,
					},
				},
			},
		},
		{
			"API and web",
			InfraSpec{
				Parameters: []Parameter{
					NewContainerAppServiceExistsParameter("api"),
					NewContainerAppServiceExistsParameter("web"),
				},
				Services: []ServiceSpec{
					ServiceSpec{
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
					ServiceSpec{
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
				Parameters: []Parameter{
					NewContainerAppServiceExistsParameter("api"),
					Parameter{
						Name:   "sqlAdminPassword",
						Value:  "$(secretOrRandomPassword)",
						Type:   "string",
						Secret: true,
					},
					Parameter{
						Name:   "appUserPassword",
						Value:  "$(secretOrRandomPassword)",
						Type:   "string",
						Secret: true,
					},
				},
				Services: []ServiceSpec{
					ServiceSpec{
						Name: "api",
						Port: 3100,
						DbPostgres: &DatabasePostgres{
							DatabaseName: "appdb",
							DatabaseUser: "appuser",
						},
					},
				},
			},
		},
		{
			"API with MongoDB",
			InfraSpec{
				DbCosmos: &DatabaseCosmos{
					DatabaseName: "appdb",
				},
				Parameters: []Parameter{
					NewContainerAppServiceExistsParameter("api"),
				},
				Services: []ServiceSpec{
					ServiceSpec{
						Name: "api",
						Port: 3100,
						DbCosmos: &DatabaseCosmos{
							DatabaseName: "appdb",
						},
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

			if testing.Short() {
				return
			}

			ctx := context.Background()
			cli, err := bicep.NewBicepCli(ctx, mockinput.NewMockConsole(), exec.NewCommandRunner(nil))
			require.NoError(t, err)

			res, err := cli.Build(ctx, filepath.Join(dir, "main.bicep"))
			require.NoError(t, err)

			require.Empty(t, res.LintErr, "lint errors occurred")
		})
	}
}
