// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// workflowService exposes features of AZD workflows to the Extensions Framework layer.
type workflowService struct {
	azdext.UnimplementedWorkflowServiceServer

	runner *workflow.Runner
}

// NewWorkflowService creates a new instance of the workflow service.
func NewWorkflowService(runner *workflow.Runner) azdext.WorkflowServiceServer {
	return &workflowService{
		runner: runner,
	}
}

// Run executes the specified workflow.
func (s *workflowService) Run(ctx context.Context, request *azdext.RunWorkflowRequest) (*azdext.EmptyResponse, error) {
	wf := request.Workflow
	if wf == nil || len(wf.Steps) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "workflow is empty")
	}

	azdWorkflow, err := convertWorkflow(wf)
	if err != nil {
		return nil, err
	}

	if err := s.runner.Run(ctx, azdWorkflow); err != nil {
		workflowErr := fmt.Errorf("failed to run workflow: %w", err)
		mappedErr := mapHostError(workflowErr)
		switch status.Code(mappedErr) {
		case codes.Canceled, codes.DeadlineExceeded:
			return nil, mappedErr
		}

		if errors.Is(workflowErr, environment.ErrExists) {
			return nil, status.Error(codes.AlreadyExists, workflowErr.Error())
		}
		return nil, status.Error(codes.Internal, workflowErr.Error())
	}

	return &azdext.EmptyResponse{}, nil
}

// convertWorkflow converts an azdext.Workflow to a workflow.Workflow.
func convertWorkflow(wf *azdext.Workflow) (*workflow.Workflow, error) {
	azdWorkflow := workflow.Workflow{
		Name:  wf.Name,
		Steps: []*workflow.Step{},
	}

	for _, step := range wf.Steps {
		if step.Command == nil || len(step.Command.Args) == 0 {
			return nil, status.Errorf(codes.InvalidArgument, "step command is empty")
		}

		azdStep := &workflow.Step{
			AzdCommand: workflow.Command{
				Args: step.Command.Args,
			},
		}

		azdWorkflow.Steps = append(azdWorkflow.Steps, azdStep)
	}

	return &azdWorkflow, nil
}
