// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"google.golang.org/grpc/codes"
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
	custom  any
}

var _ BetaProjectServiceAddServiceOverride = (*betaProjectServiceOverride)(nil)

// GetAddServiceCapabilities only advertises the built-in acknowledgment implementation.
// A custom AddService override must supply its own capability implementation to opt in.
func (o *betaProjectServiceOverride) GetAddServiceCapabilities(
	context.Context, *v1beta.EmptyRequest,
) (*v1beta.GetAddServiceCapabilitiesResponse, error) {
	_, custom := findBetaOverride[BetaProjectServiceAddServiceOverride](o.custom)
	return &v1beta.GetAddServiceCapabilitiesResponse{AcknowledgmentSupported: !custom}, nil
}

// AddService adapts the v1beta AddServiceRequest.operation_id field onto the shared mutation
// logic. On an acknowledged failure it attaches an AddServiceAcknowledgment detail to the
// returned gRPC status instead of a trailer, so the capability is discoverable from the v1beta
// service definition and generated clients rather than relying on an undocumented header name.
// Operation identifiers longer than 64 bytes are rejected before project mutation.
func (o *betaProjectServiceOverride) AddService(
	ctx context.Context, req *v1beta.AddServiceRequest,
) (*v1beta.EmptyResponse, error) {
	if len(req.GetOperationId()) > maxAddServiceOperationIDBytes {
		return nil, status.Errorf(
			codes.InvalidArgument, "operation_id must not exceed %d bytes", maxAddServiceOperationIDBytes,
		)
	}

	stableReq := new(azdext.AddServiceRequest)
	if err := transcodeBetaRequest(req, stableReq); err != nil {
		return nil, err
	}

	acknowledged, err := o.service.addService(ctx, stableReq, req.GetOperationId())
	if err != nil {
		if acknowledged {
			withDetails, detailErr := status.Convert(mapHostError(err)).WithDetails(
				&v1beta.AddServiceAcknowledgment{OperationId: req.GetOperationId()},
			)
			if detailErr == nil {
				return nil, withDetails.Err()
			}
			return nil, status.Errorf(codes.Internal,
				"AddService failed: %v; attaching completion acknowledgment: %v", err, detailErr)
		}
		return nil, err
	}

	return &v1beta.EmptyResponse{}, nil
}
