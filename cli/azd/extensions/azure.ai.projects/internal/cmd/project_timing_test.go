// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.projects/internal/inittelemetry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type projectTimingRecorder struct {
	events []foundryTelemetry.Event
}

func (r *projectTimingRecorder) Report(_ context.Context, event foundryTelemetry.Event) {
	r.events = append(r.events, event)
}

type projectTimingPromptServer struct {
	azdext.UnimplementedPromptServiceServer
	err error
}

func (s *projectTimingPromptServer) Select(
	context.Context, *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	return &azdext.SelectResponse{Value: new(int32(0))}, s.err
}

func TestProjectTimingInteractiveSuccessAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name    string
		err     error
		outcome string
	}{
		{"success", nil, "success"},
		{"cancel", status.Error(codes.Canceled, "sensitive answer"), "canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newAzureContextClient(t, &projectTimingPromptServer{err: test.err}, &azureContextAccountServer{})
			ctx, timing := inittelemetry.NewContext(t.Context(), inittelemetry.ProjectAdd, false)
			target, err := resolveProjectTarget(
				ctx, client, &azdext.ProjectConfig{}, nil, map[string]string{}, &projectAddFlags{},
			)
			if test.err == nil {
				require.NoError(t, err)
				assert.Equal(t, projectModeNew, target.Mode)
			} else {
				require.Error(t, err)
			}
			recorder := &projectTimingRecorder{}
			timing.Flush(ctx, recorder, err)
			require.Len(t, recorder.events, 2)
			assert.Equal(t, "project.add.summary", recorder.events[0].Name)
			assert.Equal(t, test.outcome, recorder.events[0].Attributes["init.flow.outcome"])
			assert.Equal(t, "1", recorder.events[0].Attributes["init.prompt.count"])
			assert.Equal(t, "project.add.step", recorder.events[1].Name)
			assert.Equal(t, "project_mode_selection", recorder.events[1].Attributes["init.step.id"])
			assert.Equal(t, "prompt", recorder.events[1].Attributes["init.step.category"])
			assert.Equal(t, test.outcome, recorder.events[1].Attributes["init.step.outcome"])
		})
	}
}

func TestProjectTimingNonInteractiveAndEarlyFailure(t *testing.T) {
	ctx, timing := inittelemetry.NewContext(t.Context(), inittelemetry.ProjectAdd, true)
	target, err := resolveProjectTarget(
		ctx, nil, &azdext.ProjectConfig{}, nil, map[string]string{}, &projectAddFlags{noPrompt: true},
	)
	require.NoError(t, err)
	assert.Equal(t, projectModeNew, target.Mode)
	action := &ProjectAddAction{flags: &projectAddFlags{
		newProject: true, projectID: "private-resource-name", noPrompt: true,
	}}
	err = action.Run(ctx)
	require.Error(t, err)
	recorder := &projectTimingRecorder{}
	timing.Flush(ctx, recorder, err)
	require.Len(t, recorder.events, 3)
	assert.Equal(t, "0", recorder.events[0].Attributes["init.prompt.count"])
	assert.Equal(t, "0.000", recorder.events[0].Attributes["init.prompt.total_duration_ms"])
	assert.Equal(t, "error", recorder.events[0].Attributes["init.flow.outcome"])
	assert.Equal(t, "default", recorder.events[1].Attributes["init.step.input_source"])
	assert.Equal(t, "input_validation", recorder.events[2].Attributes["init.step.id"])
	assert.Equal(t, "execution", recorder.events[2].Attributes["init.step.category"])
}

func TestProjectTimingPromptCoverage(t *testing.T) {
	files, err := filepath.Glob("project*.go")
	require.NoError(t, err)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		require.NoError(t, err)
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			clientCall, ok := method.X.(*ast.CallExpr)
			if !ok {
				return true
			}
			clientMethod, ok := clientCall.Fun.(*ast.SelectorExpr)
			if ok && clientMethod.Sel.Name == "Prompt" && len(clientCall.Args) == 0 {
				t.Errorf("%s: uninstrumented prompt RPC %s", path, method.Sel.Name)
			}
			return true
		})
	}
}
