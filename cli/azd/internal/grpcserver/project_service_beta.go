// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"google.golang.org/grpc/status"
)

type betaProjectServiceOverride struct {
	service *projectService
}

var _ BetaProjectServiceAddServiceOverride = (*betaProjectServiceOverride)(nil)

// AddService reports completed failures using a typed beta status detail.
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
			withDetails, detailErr := status.Convert(err).WithDetails(
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
