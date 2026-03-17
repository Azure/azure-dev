// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_WorkflowService_Run_Success(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	contextType := mock.AnythingOfType("*context.cancelCtx")

	t.Run("Success", func(t *testing.T) {
		testRunner := &TestWorkflowRunner{}
		runner := workflow.NewRunner(testRunner, mockContext.Console)
		testRunner.On("ExecuteContext", contextType, mock.Anything).Return(nil)

		service := NewWorkflowService(runner)

		// Create a valid, non-empty workflow.
		req := &azdext.RunWorkflowRequest{
			Workflow: &azdext.Workflow{
				Name: "testWorkflow",
				Steps: []*azdext.WorkflowStep{
					{
						Command: &azdext.WorkflowCommand{
							Args: []string{"provision"},
						},
					},
				},
			},
		}

		// Act
		resp, err := service.Run(*mockContext.Context, req)

		// Assert
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Verify that the runner's ExecuteContext was invoked with the correct args.
		testRunner.AssertCalled(t, "ExecuteContext", contextType, []string{"provision"})
	})

	t.Run("Failure", func(t *testing.T) {
		expectedErr := errors.New("execution failed")
		testRunner := &TestWorkflowRunner{}
		runner := workflow.NewRunner(testRunner, mockContext.Console)
		testRunner.On("ExecuteContext", contextType, mock.Anything).Return(expectedErr)

		service := NewWorkflowService(runner)

		// Create a valid, non-empty workflow.
		req := &azdext.RunWorkflowRequest{
			Workflow: &azdext.Workflow{
				Name: "testWorkflow",
				Steps: []*azdext.WorkflowStep{
					{
						Command: &azdext.WorkflowCommand{
							Args: []string{"provision"},
						},
					},
				},
			},
		}

		// Act
		resp, err := service.Run(*mockContext.Context, req)

		// Assert
		require.Error(t, err)
		require.Nil(t, resp)

		// Verify that the runner's ExecuteContext was invoked with the correct args.
		testRunner.AssertCalled(t, "ExecuteContext", contextType, []string{"provision"})
	})
}

// Updated TestWorkflowRunner using testify/mock.
type TestWorkflowRunner struct {
	mock.Mock
}

// ExecuteContext implements workflow.AzdCommandRunner using testify/mock.
func (r *TestWorkflowRunner) ExecuteContext(ctx context.Context, args []string) error {
	ret := r.Called(ctx, args)
	return ret.Error(0)
}
