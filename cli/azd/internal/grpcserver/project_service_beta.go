// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"google.golang.org/grpc/status"
)

// betaProjectServiceOverride implements the v1beta typed AddServiceAcknowledgment contract
// (requested in PR review as a discoverable, generated-client-visible replacement for the v1
// azd-project-add-service-operation gRPC metadata/trailer convention). It shares the same
// mutation logic as the v1 handler (projectService.addService); only the operation-identifier
// transport and the acknowledgment surface differ between channels. See the "Project service
// save acknowledgment" section of docs/architecture/extension-framework.md.
type betaProjectServiceOverride struct {
	service *projectService
}

var _ BetaProjectServiceAddServiceOverride = (*betaProjectServiceOverride)(nil)

// AddService adapts the v1beta AddServiceRequest.operation_id field onto the shared mutation
// logic. On an acknowledged failure it attaches an AddServiceAcknowledgment detail to the
// returned gRPC status instead of a trailer, so the capability is discoverable from the v1beta
// service definition and generated clients rather than relying on an undocumented header name.
func (o *betaProjectServiceOverride) AddService(
	ctx context.Context, req *v1beta.AddServiceRequest,
) (*v1beta.EmptyResponse, error) {
	stableReq := new(azdext.AddServiceRequest)
	if err := transcodeBetaRequest(req, stableReq); err != nil {
		return nil, err
	}

	acknowledged, err := o.service.addService(ctx, stableReq, req.GetOperationId())
	if err != nil {
		if acknowledged {
			if withDetails, detailErr := status.Convert(err).WithDetails(
				&v1beta.AddServiceAcknowledgment{OperationId: req.GetOperationId()},
			); detailErr == nil {
				return nil, withDetails.Err()
			}
			// Attaching the detail failed; return the original acknowledged error rather than
			// silently downgrading to an unacknowledged one.
		}
		return nil, err
	}

	return &v1beta.EmptyResponse{}, nil
}
