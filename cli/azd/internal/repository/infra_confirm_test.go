// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package repository

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/stretchr/testify/require"
)

func TestInitializer_infraSpecFromDetect(t *testing.T) {
	t.Parallel()
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
						Host:    "containerapp",
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
						Host:     "containerapp",
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
						Host:    "containerapp",
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
						Host: "containerapp",
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
						Host: "containerapp",
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
				"myappdb", // fill in db name
			},
			want: scaffold.InfraSpec{
				DbPostgres: &scaffold.DatabasePostgres{
					DatabaseName: "myappdb",
				},
				KeyVault: &scaffold.KeyVault{},
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
							DatabaseName: "myappdb",
						},
						Host: "containerapp",
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
						Host: "containerapp",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := &Initializer{
				console: newCapturedTestConsole(t, tt.interactions),
			}

			spec, err := i.infraSpecFromDetect(t.Context(), tt.detect)

			require.NoError(t, err)
			require.Equal(t, tt.want, spec)
		})
	}
}
