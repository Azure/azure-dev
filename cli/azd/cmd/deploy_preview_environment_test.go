// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	azdcmd "github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestCommandEnvironmentServiceUsesSnapshotsOnlyForPreview(t *testing.T) {
	for _, preview := range []bool{false, true} {
		name := "normal-command"
		if preview {
			name = "deployment-preview"
		}
		t.Run(name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())
			store := environment.NewLocalFileDataStore(azdCtx, config.NewFileConfigManager(config.NewManager()))
			manager, err := environment.NewManager(
				mockContext.Container, azdCtx, mockContext.Console, store, nil,
			)
			require.NoError(t, err)
			env, err := manager.Create(t.Context(), environment.Spec{Name: "selected"})
			require.NoError(t, err)
			env.DotenvSet("KEY", "before")
			require.NoError(t, manager.Save(t.Context(), env))
			require.NoError(t, azdCtx.SetProjectState(azdcontext.ProjectState{DefaultEnvironment: env.Name()}))
			snapshot, err := manager.GetReadOnly(t.Context(), env.Name())
			require.NoError(t, err)

			command := &cobra.Command{Use: "env"}
			if preview {
				command = azdcmd.NewDeployCmd()
				azdcmd.NewDeployFlags(command, &internal.GlobalCommandOptions{})
				require.NoError(t, command.Flags().Set("preview", "true"))
			}
			service := newCommandEnvironmentService(command, lazy.From(azdCtx), lazy.From(manager), lazy.From(snapshot))
			current, err := service.GetCurrent(t.Context(), &azdext.EmptyRequest{})
			require.NoError(t, err)
			require.Equal(t, "selected", current.Environment.Name)

			env.DotenvSet("KEY", "after")
			require.NoError(t, manager.Save(t.Context(), env))
			value, err := service.GetValue(t.Context(), &azdext.GetEnvRequest{EnvName: env.Name(), Key: "KEY"})
			require.NoError(t, err)
			expected := "after"
			if preview {
				expected = "before"
			}
			require.Equal(t, expected, value.Value)
		})
	}
}
