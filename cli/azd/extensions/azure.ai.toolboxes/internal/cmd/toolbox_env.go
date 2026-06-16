// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/foundry/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// setToolboxEndpointEnvFunc is a seam so the create/delete cores can be
// unit-tested without a live azd gRPC server.
var setToolboxEndpointEnvFunc = setToolboxEndpointEnv

// setToolboxEndpointEnv writes value under the toolbox's MCP-endpoint key. An
// empty value clears it (there is no RPC to delete a .env key, so delete blanks
// instead). Best-effort: a missing azd daemon (direct `azd x` run, not inside a
// project) is skipped rather than failing the toolbox operation.
func setToolboxEndpointEnv(ctx context.Context, toolboxName, value string) error {
	key := envkey.ToolboxMCPEndpoint(toolboxName)

	return withAzdClient(func(c *azdext.AzdClient) error {
		envResp, err := c.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil {
			if containsGRPCCode(err, codes.Unavailable) {
				log.Printf("toolbox env sync: azd environment unavailable, skipping %s", key)
				return nil
			}
			return exterrors.Internal(
				exterrors.CodeAzdClientFailed,
				fmt.Sprintf("failed to read current azd environment: %s", err),
			)
		}

		if _, err := c.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envResp.Environment.Name,
			Key:     key,
			Value:   value,
		}); err != nil {
			return exterrors.Internal(
				exterrors.CodeAzdClientFailed,
				fmt.Sprintf("failed to set %s in the azd environment: %s", key, err),
			)
		}
		return nil
	})
}

// containsGRPCCode walks the error chain for a gRPC status with the given code.
// fmt.Errorf("%w", ...) drops the GRPCStatus() method, so we unwrap manually
// (errors.Join multi-wraps are not traversed).
func containsGRPCCode(err error, code codes.Code) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if st, ok := status.FromError(err); ok && st.Code() == code {
			return true
		}
	}
	return false
}
