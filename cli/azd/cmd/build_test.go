// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/guidance"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
)

type commandRunnerFunc func(context.Context, []string) error

func (r commandRunnerFunc) ExecuteContext(ctx context.Context, args []string) error {
	return r(ctx, args)
}

func Test_NewBuildAction_Constructor(t *testing.T) {
	t.Parallel()
	flags := &buildFlags{}
	args := []string{"svc"}
	console := mockinput.NewMockConsole()
	formatter := &output.JsonFormatter{}
	a := newBuildAction(
		flags, args, nil, nil, nil, nil, console, formatter, io.Discard, nil,
	)
	ba := a.(*buildAction)
	require.Same(t, flags, ba.flags)
	require.Equal(t, args, ba.args)
}

func Test_NewBuildCmd(t *testing.T) {
	t.Parallel()
	cmd := newBuildCmd()
	require.NotNil(t, cmd)
	assert.Equal(t, "build <service>", cmd.Use)
}

func Test_NewBuildFlags(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "test"}
	global := &internal.GlobalCommandOptions{}
	flags := newBuildFlags(cmd, global)
	require.NotNil(t, flags)
}

func TestBuildAction_StandaloneRestoreAndBuildUseSameCommandOrder(t *testing.T) {
	tests := []struct {
		name       string
		postbuild  string
		wantFollow string
	}{
		{
			name:       "postbuild replaces postrestore",
			postbuild:  "build follow-up",
			wantFollow: "build follow-up",
		},
		{
			name:      "postbuild clears postrestore",
			postbuild: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := guidance.NewFollowUpCollector()
			eventOrders := make(map[string]uint64)
			restoreOrder := uint64(0)
			projectConfig := &project.ProjectConfig{
				Services: map[string]*project.ServiceConfig{},
				EventDispatcher: ext.NewEventDispatcher[project.ProjectLifecycleEventArgs](
					project.ProjectEventRestore,
					project.ProjectEventBuild,
				),
			}
			require.NoError(t, projectConfig.EventDispatcher.AddHandler(
				t.Context(),
				ext.Event("postrestore"),
				func(ctx context.Context, _ project.ProjectLifecycleEventArgs) error {
					order := guidance.FollowUpCommandOrderFromContext(ctx)
					eventOrders["postrestore"] = order
					collector.Add(guidance.FollowUp{
						ExtensionID:  "test.extension",
						CommandOrder: order,
						EventName:    "postrestore",
						Text:         "restore follow-up",
					})
					return nil
				},
			))
			require.NoError(t, projectConfig.EventDispatcher.AddHandler(
				t.Context(),
				ext.Event("postbuild"),
				func(ctx context.Context, _ project.ProjectLifecycleEventArgs) error {
					order := guidance.FollowUpCommandOrderFromContext(ctx)
					eventOrders["postbuild"] = order
					collector.Add(guidance.FollowUp{
						ExtensionID:  "test.extension",
						CommandOrder: order,
						EventName:    "postbuild",
						Text:         tt.postbuild,
					})
					return nil
				},
			))
			projectManager := &mockProjectManager{}
			projectManager.On("InitializeServices", mock.Anything, mock.Anything).
				Return(nil).Once()
			projectManager.On("EnsureFrameworkTools", mock.Anything, mock.Anything).
				Return(nil).Once()
			runner := workflow.NewRunner(commandRunnerFunc(func(
				ctx context.Context,
				args []string,
			) error {
				require.Equal(t, []string{"restore", "--all"}, args)
				restoreOrder = guidance.FollowUpCommandOrderFromContext(ctx)
				return projectConfig.Invoke(
					ctx,
					project.ProjectEventRestore,
					project.ProjectLifecycleEventArgs{Project: projectConfig},
					func() error { return nil },
				)
			}), nil)
			action := &buildAction{
				flags:          &buildFlags{all: true},
				projectConfig:  projectConfig,
				projectManager: projectManager,
				importManager:  project.NewImportManager(nil),
				console:        mockinput.NewMockConsole(),
				formatter:      &output.JsonFormatter{},
				writer:         io.Discard,
				workflowRunner: runner,
			}
			ctx := guidance.WithFollowUpCollector(t.Context(), collector)

			_, err := action.Run(ctx)
			require.NoError(t, err)
			require.Equal(t, uint64(1), restoreOrder)
			require.Equal(t, map[string]uint64{
				"postrestore": restoreOrder,
				"postbuild":   restoreOrder,
			}, eventOrders)
			require.Equal(t, tt.wantFollow, collector.Text())
			projectManager.AssertExpectations(t)
		})
	}
}
