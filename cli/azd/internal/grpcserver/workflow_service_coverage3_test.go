// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestWorkflowService_Run_NilWorkflow(t *testing.T) {
	t.Parallel()
	svc := NewWorkflowService(nil)
	_, err := svc.Run(t.Context(), &azdext.RunWorkflowRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "workflow is empty")
}

func TestWorkflowService_Run_EmptySteps(t *testing.T) {
	t.Parallel()
	svc := NewWorkflowService(nil)
	_, err := svc.Run(t.Context(), &azdext.RunWorkflowRequest{
		Workflow: &azdext.Workflow{Name: "test", Steps: []*azdext.WorkflowStep{}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "workflow is empty")
}

func TestWorkflowService_Run_StepNilCommand(t *testing.T) {
	t.Parallel()
	svc := NewWorkflowService(nil)
	_, err := svc.Run(t.Context(), &azdext.RunWorkflowRequest{
		Workflow: &azdext.Workflow{
			Name: "test",
			Steps: []*azdext.WorkflowStep{
				{Command: nil},
			},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "step command is empty")
}

func TestWorkflowService_Run_StepEmptyArgs(t *testing.T) {
	t.Parallel()
	svc := NewWorkflowService(nil)
	_, err := svc.Run(t.Context(), &azdext.RunWorkflowRequest{
		Workflow: &azdext.Workflow{
			Name: "test",
			Steps: []*azdext.WorkflowStep{
				{Command: &azdext.WorkflowCommand{Args: []string{}}},
			},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "step command is empty")
}
