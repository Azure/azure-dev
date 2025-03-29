// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/workflow"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type WorkflowService struct {
	azdext.UnimplementedWorkflowServiceServer

	runner *workflow.Runner
}

func NewWorkflowService(runner *workflow.Runner) azdext.WorkflowServiceServer {
	return &WorkflowService{
		runner: runner,
	}
}

func (s *WorkflowService) Run(ctx context.Context, request *azdext.RunWorkflowRequest) (*azdext.EmptyResponse, error) {
	wf := request.Workflow
	if wf == nil || len(wf.Steps) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "workflow is empty")
	}

	azdWorkflow, err := convertWorkflow(wf)
	if err != nil {
		return nil, err
	}

	if err := s.runner.Run(ctx, azdWorkflow); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to run workflow: %v", err)
	}

	return &azdext.EmptyResponse{}, nil
}

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
