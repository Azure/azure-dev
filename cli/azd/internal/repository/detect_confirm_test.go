package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/appdetect"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_detectConfirm_confirm(t *testing.T) {
	dir := t.TempDir()
	// avoid symlinked paths as this may result in the final path returned
	// to be a valid, but aliased path to the absolute entries in the test,
	// which fails the test's path equality assertions.
	//
	// This issue occurs on macOS where TempDir returned is symlinked to /private/var.
	dir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	dotNetDir := filepath.Join(dir, "dotnet-dir")
	err = os.MkdirAll(dotNetDir, 0700)
	require.NoError(t, err)

	javaDir := filepath.Join(dir, "java-dir")
	err = os.MkdirAll(javaDir, 0700)
	require.NoError(t, err)

	ostest.Chdir(t, dir)

	tests := []struct {
		name         string
		detection    []appdetect.Project
		interactions []string
		want         []appdetect.Project
	}{
		{
			name:      "add from empty",
			detection: []appdetect.Project{},
			interactions: []string{
				"y",
				fmt.Sprintf("%s\t%s", appdetect.Java.Display(), "[Language]"),
				"java-dir",
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language:      appdetect.Java,
					Path:          javaDir,
					DetectionRule: string(EntryKindManual),
				},
			},
		},
		{
			name: "confirm single",
			detection: []appdetect.Project{
				{
					Language: appdetect.DotNet,
					Path:     dotNetDir,
				},
			},
			interactions: []string{
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.DotNet,
					Path:     dotNetDir,
				},
			},
		},
		{
			name: "confirm single with storage resource",
			detection: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					AzureDeps: []appdetect.AzureDep{
						appdetect.AzureDepStorageAccount{
							ContainerNamePropertyMap: map[string]string{
								"spring.cloud.azure.container": "container1",
							},
						},
					},
				},
			},
			interactions: []string{
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					AzureDeps: []appdetect.AzureDep{
						appdetect.AzureDepStorageAccount{
							ContainerNamePropertyMap: map[string]string{
								"spring.cloud.azure.container": "container1",
							},
						},
					},
				},
			},
		},
		{
			name: "confirm single with resources service bus",
			detection: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					AzureDeps: []appdetect.AzureDep{
						appdetect.AzureDepServiceBus{
							Queues: []string{"queue1"},
							IsJms:  true,
						},
					},
				},
			},
			interactions: []string{
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					AzureDeps: []appdetect.AzureDep{
						appdetect.AzureDepServiceBus{
							Queues: []string{"queue1"},
							IsJms:  true,
						},
					},
				},
			},
		},
		{
			name: "confirm single with event hubs resource",
			detection: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					AzureDeps: []appdetect.AzureDep{
						appdetect.AzureDepEventHubs{
							EventHubsNamePropertyMap: map[string]string{
								"spring.cloud.azure.eventhubs": "eventhub1",
							},
						},
					},
				},
			},
			interactions: []string{
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					AzureDeps: []appdetect.AzureDep{
						appdetect.AzureDepEventHubs{
							EventHubsNamePropertyMap: map[string]string{
								"spring.cloud.azure.eventhubs": "eventhub1",
							},
						},
					},
				},
			},
		},
		{
			name: "confirm single with cosmos db resource",
			detection: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					DatabaseDeps: []appdetect.DatabaseDep{
						appdetect.DbCosmos,
					},
				},
			},
			interactions: []string{
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					DatabaseDeps: []appdetect.DatabaseDep{
						appdetect.DbCosmos,
					},
				},
			},
		},
		{
			name: "confirm single with postgresql resource",
			detection: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					DatabaseDeps: []appdetect.DatabaseDep{
						appdetect.DbPostgres,
					},
				},
			},
			interactions: []string{
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					DatabaseDeps: []appdetect.DatabaseDep{
						appdetect.DbPostgres,
					},
				},
			},
		},
		{
			name: "confirm single with mysql resource",
			detection: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					DatabaseDeps: []appdetect.DatabaseDep{
						appdetect.DbMySql,
					},
				},
			},
			interactions: []string{
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					DatabaseDeps: []appdetect.DatabaseDep{
						appdetect.DbMySql,
					},
				},
			},
		},
		{
			name: "confirm single with cosmos db mongo resource",
			detection: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					DatabaseDeps: []appdetect.DatabaseDep{
						appdetect.DbMongo,
					},
				},
			},
			interactions: []string{
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.Java,
					Path:     javaDir,
					DatabaseDeps: []appdetect.DatabaseDep{
						appdetect.DbMongo,
					},
				},
			},
		},
		{
			name: "add a language",
			detection: []appdetect.Project{
				{
					Language: appdetect.DotNet,
					Path:     dotNetDir,
				},
			},
			interactions: []string{
				"Add an undetected service",
				fmt.Sprintf("%s\t%s", appdetect.Java.Display(), "[Language]"),
				"java-dir",
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.DotNet,
					Path:     dotNetDir,
				},
				{
					Language:      appdetect.Java,
					Path:          javaDir,
					DetectionRule: string(EntryKindManual),
				},
			},
		},
		{
			name: "add a framework",
			detection: []appdetect.Project{
				{
					Language: appdetect.DotNet,
					Path:     dotNetDir,
				},
			},
			interactions: []string{
				"Add an undetected service",
				fmt.Sprintf("%s\t%s", appdetect.JsReact.Display(), "[Framework]"),
				"java-dir",
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.DotNet,
					Path:     dotNetDir,
				},
				{
					Language:      appdetect.JavaScript,
					Path:          javaDir,
					Dependencies:  []appdetect.Dependency{appdetect.JsReact},
					DetectionRule: string(EntryKindManual),
				},
			},
		},
		{
			name: "remove a language",
			detection: []appdetect.Project{
				{
					Language: appdetect.DotNet,
					Path:     dotNetDir,
				},
			},
			interactions: []string{
				"Remove a detected service",
				fmt.Sprintf("%s in %s", appdetect.DotNet.Display(), "dotnet-dir"),
				"y",
				"Add an undetected service",
				fmt.Sprintf("%s\t%s", appdetect.Java.Display(), "[Language]"),
				"java-dir",
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language:      appdetect.Java,
					Path:          javaDir,
					DetectionRule: string(EntryKindManual),
				},
			},
		},
		{
			name: "add a database",
			detection: []appdetect.Project{
				{
					Language: appdetect.DotNet,
					Path:     dotNetDir,
				},
			},
			interactions: []string{
				"Add an undetected service",
				fmt.Sprintf("%s\t%s", appdetect.DbPostgres.Display(), "[Database]"),
				fmt.Sprintf("%s in %s", appdetect.DotNet.Display(), "dotnet-dir"),
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language: appdetect.DotNet,
					Path:     dotNetDir,
					DatabaseDeps: []appdetect.DatabaseDep{
						appdetect.DbPostgres,
					},
					DetectionRule: string(EntryKindModified),
				},
			},
		},
		{
			name: "remove a database",
			detection: []appdetect.Project{
				{
					Language: appdetect.DotNet,
					Path:     dotNetDir,
					DatabaseDeps: []appdetect.DatabaseDep{
						appdetect.DbMongo,
					},
				},
			},
			interactions: []string{
				"Remove a detected service",
				appdetect.DbMongo.Display(),
				"y",
				"Confirm and continue initializing my app",
			},
			want: []appdetect.Project{
				{
					Language:      appdetect.DotNet,
					Path:          dotNetDir,
					DatabaseDeps:  []appdetect.DatabaseDep{},
					DetectionRule: string(EntryKindModified),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &detectConfirm{
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
			d.Init(tt.detection, dir)

			err = d.Confirm(context.Background())

			// Print extra newline to avoid mangling `go test -v` final test result output while waiting for final stdin,
			// which may result in incorrect `gotestsum` reporting
			fmt.Println()

			require.NoError(t, err)
			require.Equal(t, tt.want, d.Services)
		})
	}
}
