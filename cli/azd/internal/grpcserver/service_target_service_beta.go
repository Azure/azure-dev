// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Preview dispatch belongs to the CLI follow-up. Until then, reject beta capability
// registration explicitly instead of acknowledging it through a stable handler that drops it.
type betaServiceTargetPreviewOverride struct {
	stable azdext.ServiceTargetServiceServer
}

var _ BetaServiceTargetServiceStreamOverride = (*betaServiceTargetPreviewOverride)(nil)

func (s *betaServiceTargetPreviewOverride) Stream(
	stream grpc.BidiStreamingServer[v1beta.ServiceTargetMessage, v1beta.ServiceTargetMessage],
) error {
	return (&betaServiceTargetServiceAdapter{stable: s.stable}).Stream(&previewRegistrationGuard{stream})
}

type previewRegistrationGuard struct {
	grpc.BidiStreamingServer[v1beta.ServiceTargetMessage, v1beta.ServiceTargetMessage]
}

func (s *previewRegistrationGuard) Recv() (*v1beta.ServiceTargetMessage, error) {
	message, err := s.BidiStreamingServer.Recv()
	if err != nil {
		return nil, err
	}
	if message.GetRegisterServiceTargetRequest().GetSupportsPreview() {
		return nil, status.Error(
			codes.Unimplemented, "this azd host does not implement beta service target deployment preview",
		)
	}
	return message, nil
}
