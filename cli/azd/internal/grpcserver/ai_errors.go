// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func aiStatusError(code codes.Code, reason string, message string, metadata map[string]string) error {
	st := status.New(code, message)
	withDetails, err := st.WithDetails(&errdetails.ErrorInfo{
		Reason:   reason,
		Domain:   azdext.AiErrorDomain,
		Metadata: metadata,
	})
	if err != nil {
		return st.Err()
	}

	return withDetails.Err()
}

func mapAiResolveError(err error, modelName string) error {
	switch {
	case errors.Is(err, ai.ErrQuotaLocationRequired):
		return aiStatusError(
			codes.InvalidArgument,
			azdext.AiErrorReasonQuotaLocation,
			err.Error(),
			nil,
		)
	case errors.Is(err, ai.ErrModelNotFound):
		return aiStatusError(
			codes.NotFound,
			azdext.AiErrorReasonModelNotFound,
			err.Error(),
			map[string]string{"model_name": modelName},
		)
	case errors.Is(err, ai.ErrNoDeploymentMatch):
		return aiStatusError(
			codes.FailedPrecondition,
			azdext.AiErrorReasonNoDeploymentMatch,
			err.Error(),
			map[string]string{"model_name": modelName},
		)
	default:
		return fmt.Errorf("resolving model deployments: %w", err)
	}
}
