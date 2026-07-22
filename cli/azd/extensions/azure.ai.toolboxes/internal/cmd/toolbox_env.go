// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"strings"

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
// instead). Best-effort: when no azd environment is in play (no daemon, or no
// default environment selected) the write is skipped rather than failing the
// toolbox operation.
func setToolboxEndpointEnv(ctx context.Context, toolboxName, value string) error {
	key := envkey.ToolboxMCPEndpoint(toolboxName)

	return withAzdClient(func(c *azdext.AzdClient) error {
		envResp, err := c.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil {
			if isNoAzdEnvironment(err) {
				log.Printf("toolbox env sync: no azd environment, skipping %s", key)
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

// isNoAzdEnvironment reports whether err from GetCurrent means there is no azd
// environment to write to: the daemon is unreachable (codes.Unavailable), no
// default environment is selected, or no azd project exists. The host returns
// the latter two as status-less sentinels that gRPC surfaces as codes.Unknown,
// so we match on the message text (as azure.ai.agents does).
func isNoAzdEnvironment(err error) bool {
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	if st.Code() == codes.Unavailable {
		return true
	}
	msg := strings.ToLower(st.Message())
	return strings.Contains(msg, "default environment not found") ||
		strings.Contains(msg, "no project exists")
}
