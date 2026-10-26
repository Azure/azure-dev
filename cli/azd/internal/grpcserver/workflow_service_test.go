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

func TestWorkflowService_Run_PreservesCancellationAndDeadline(t *testing.T) {
	tests := []struct {
		name         string
		runnerErr    error
		wantGrpcCode codes.Code
		wantCategory azdext.LocalErrorCategory
		wantCode     string
		wantCause    error
	}{
		{
			name:         "Canceled",
			runnerErr:    context.Canceled,
			wantGrpcCode: codes.Canceled,
			wantCategory: azdext.LocalErrorCategoryUser,
			wantCode:     "canceled",
			wantCause:    context.Canceled,
		},
		{
			name:         "WrappedCanceled",
			runnerErr:    fmt.Errorf("command canceled: %w", context.Canceled),
			wantGrpcCode: codes.Canceled,
			wantCategory: azdext.LocalErrorCategoryUser,
			wantCode:     "canceled",
			wantCause:    context.Canceled,
		},
		{
			name:         "GrpcCanceled",
			runnerErr:    status.Error(codes.Canceled, "operation canceled"),
			wantGrpcCode: codes.Canceled,
			wantCategory: azdext.LocalErrorCategoryUser,
			wantCode:     "canceled",
			wantCause:    context.Canceled,
		},
		{
			name:         "DeadlineExceeded",
			runnerErr:    context.DeadlineExceeded,
			wantGrpcCode: codes.DeadlineExceeded,
			wantCategory: azdext.LocalErrorCategoryInternal,
			wantCode:     "deadline_exceeded",
			wantCause:    context.DeadlineExceeded,
		},
		{
			name:         "WrappedDeadlineExceeded",
			runnerErr:    fmt.Errorf("command timed out: %w", context.DeadlineExceeded),
			wantGrpcCode: codes.DeadlineExceeded,
			wantCategory: azdext.LocalErrorCategoryInternal,
			wantCode:     "deadline_exceeded",
			wantCause:    context.DeadlineExceeded,
		},
		{
			name:         "GrpcDeadlineExceeded",
			runnerErr:    status.Error(codes.DeadlineExceeded, "operation timed out"),
			wantGrpcCode: codes.DeadlineExceeded,
			wantCategory: azdext.LocalErrorCategoryInternal,
			wantCode:     "deadline_exceeded",
			wantCause:    context.DeadlineExceeded,
		},
		{
			name:         "CanceledWinsOverFailure",
			runnerErr:    errors.Join(errors.New("execution failed"), context.Canceled),
			wantGrpcCode: codes.Canceled,
			wantCategory: azdext.LocalErrorCategoryUser,
			wantCode:     "canceled",
			wantCause:    context.Canceled,
		},
		{
			name:         "CanceledWinsOverEnvironmentAlreadyExists",
			runnerErr:    errors.Join(environment.ErrExists, context.Canceled),
			wantGrpcCode: codes.Canceled,
			wantCategory: azdext.LocalErrorCategoryUser,
			wantCode:     "canceled",
			wantCause:    context.Canceled,
		},
		{
			name:         "DeadlineWinsOverFailure",
			runnerErr:    errors.Join(errors.New("execution failed"), context.DeadlineExceeded),
			wantGrpcCode: codes.DeadlineExceeded,
			wantCategory: azdext.LocalErrorCategoryInternal,
			wantCode:     "deadline_exceeded",
			wantCause:    context.DeadlineExceeded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			contextType := mock.AnythingOfType("*context.cancelCtx")
			testRunner := &TestWorkflowRunner{}
			runner := workflow.NewRunner(testRunner, mockContext.Console)
			testRunner.On("ExecuteContext", contextType, mock.Anything).Return(tt.runnerErr)

			service := NewWorkflowService(runner)
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

			resp, err := service.Run(*mockContext.Context, req)

			require.Error(t, err)
			require.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			require.Equal(t, tt.wantGrpcCode, st.Code())

			relayed := azdext.ExtensionErrorFromStatus(st)
			require.NotNil(t, relayed)
			localErr, ok := errors.AsType[*azdext.LocalError](azdext.UnwrapError(relayed))
			require.True(t, ok)
			require.Equal(t, tt.wantCategory, localErr.Category)
			require.Equal(t, tt.wantCode, localErr.Code)
			require.ErrorIs(t, localErr, tt.wantCause)

			testRunner.AssertCalled(t, "ExecuteContext", contextType, []string{"provision"})
		})
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
