// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type betaProjectServiceOverride struct {
	service *projectService
	custom  any
}

var _ BetaProjectServiceAddServiceOverride = (*betaProjectServiceOverride)(nil)

// GetAddServiceCapabilities advertises only the built-in acknowledgment implementation.
// A custom AddService override must explicitly supply its own capability override to opt in.
func (o *betaProjectServiceOverride) GetAddServiceCapabilities(
	context.Context, *v1beta.EmptyRequest,
) (*v1beta.GetAddServiceCapabilitiesResponse, error) {
	_, custom := findBetaOverride[BetaProjectServiceAddServiceOverride](o.custom)
	return &v1beta.GetAddServiceCapabilitiesResponse{AcknowledgmentSupported: !custom}, nil
}

// AddService reports completed failures using a typed beta status detail.
// Operation identifiers over 64 bytes are rejected before project mutation.
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
			if detailErr != nil {
				return nil, fmt.Errorf("%w; attaching AddService acknowledgment: %w", err, detailErr)
			}
			return nil, withDetails.Err()
		}
		return nil, err
	}

	return &v1beta.EmptyResponse{}, nil
}
