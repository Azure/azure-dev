// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func Test_WorkflowService_Run_Success(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
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
		require.Equal(t, codes.Internal, status.Code(err))
		require.Nil(t, resp)

		// Verify that the runner's ExecuteContext was invoked with the correct args.
		testRunner.AssertCalled(t, "ExecuteContext", contextType, []string{"provision"})
	})

	t.Run("EnvironmentAlreadyExists", func(t *testing.T) {
		envExistsErr := fmt.Errorf("creating environment 'myenv': %w", environment.ErrExists)
		testRunner := &TestWorkflowRunner{}
		runner := workflow.NewRunner(testRunner, mockContext.Console)
		testRunner.On("ExecuteContext", contextType, mock.Anything).Return(envExistsErr)

		service := NewWorkflowService(runner)

		req := &azdext.RunWorkflowRequest{
			Workflow: &azdext.Workflow{
				Name: "env new",
				Steps: []*azdext.WorkflowStep{
					{Command: &azdext.WorkflowCommand{Args: []string{"env", "new", "myenv"}}},
				},
			},
		}

		resp, err := service.Run(*mockContext.Context, req)

		require.Error(t, err)
		require.Equal(t, codes.AlreadyExists, status.Code(err))
		require.Nil(t, resp)
	})
}

func Test_WorkflowService_Run_PreservesStructuredErrorDetail(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	expectedErr := &azdext.ServiceError{
		Message:     "request failed",
		ErrorCode:   "RequestFailed",
		StatusCode:  422,
		ServiceName: "example.azure.com",
		Suggestion:  "Check the request and try again",
		Links: []errorhandler.ErrorLink{{
			URL:   "https://aka.ms/request-failed",
			Title: "Request failure help",
		}},
	}
	testRunner := &TestWorkflowRunner{}
	testRunner.On("ExecuteContext", mock.Anything, mock.Anything).Return(expectedErr)
	service := NewWorkflowService(workflow.NewRunner(testRunner, mockContext.Console))

	_, err := service.Run(*mockContext.Context, validWorkflowRequest())

	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Len(t, st.Details(), 1)
	detail, ok := st.Details()[0].(*azdext.WorkflowErrorDetail)
	require.True(t, ok)
	require.NotNil(t, detail.GetError())
	require.Equal(t, "request failed", detail.GetError().GetMessage())
	require.Equal(t, azdext.ErrorOrigin_ERROR_ORIGIN_SERVICE, detail.GetError().GetOrigin())
	require.Equal(t, "Check the request and try again", detail.GetError().GetSuggestion())
	require.Equal(t, "https://aka.ms/request-failed", detail.GetError().GetLinks()[0].GetUrl())
	require.Equal(t, "Request failure help", detail.GetError().GetLinks()[0].GetTitle())
	require.Equal(t, "RequestFailed", detail.GetError().GetServiceError().GetErrorCode())
	require.Equal(t, int32(422), detail.GetError().GetServiceError().GetStatusCode())
	require.Equal(t, "example.azure.com", detail.GetError().GetServiceError().GetServiceName())
}

func Test_WorkflowService_Run_ContextErrorsUseStandardCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "Canceled", err: context.Canceled, code: codes.Canceled},
		{name: "DeadlineExceeded", err: context.DeadlineExceeded, code: codes.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			testRunner := &TestWorkflowRunner{}
			testRunner.On("ExecuteContext", mock.Anything, mock.Anything).Return(fmt.Errorf("step failed: %w", tt.err))
			service := NewWorkflowService(workflow.NewRunner(testRunner, mockContext.Console))

			_, err := service.Run(*mockContext.Context, validWorkflowRequest())

			require.Error(t, err)
			require.Equal(t, tt.code, status.Code(err))
		})
	}
}

func validWorkflowRequest() *azdext.RunWorkflowRequest {
	return &azdext.RunWorkflowRequest{
		Workflow: &azdext.Workflow{
			Name: "testWorkflow",
			Steps: []*azdext.WorkflowStep{{
				Command: &azdext.WorkflowCommand{Args: []string{"provision"}},
			}},
		},
	}
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
