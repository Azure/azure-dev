// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"strings"

	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type runtimeContext struct {
	Context    context.Context
	Client     *azdext.AzdClient
	Credential *azdext.TokenProvider
	Logger     *azdext.Logger
}

func newRuntimeContext(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, tenantID string,
) (*runtimeContext, error) {
	ctx := cmd.Context()
	if err := ctx.Err(); err != nil {
		return nil, exterrors.FromError(err)
	}
	if strings.TrimSpace(os.Getenv("AZD_SERVER")) == "" || strings.TrimSpace(os.Getenv("AZD_ACCESS_TOKEN")) == "" {
		return nil, exterrors.HostUnavailable()
	}

	logger := azdext.NewLogger("cu", azdext.LoggerOptions{Debug: extCtx.Debug, Writer: cmd.ErrOrStderr()})
	logger.Debug("Initializing azd runtime")
	ctx = azdext.WithAccessToken(ctx)
	client, err := azdext.NewAzdClient()
	if err != nil {
		logger.Debug("Failed to create azd host client")
		return nil, exterrors.HostUnavailable()
	}
	if tenantID == "" {
		deployment, err := client.Deployment().GetDeploymentContext(ctx, &azdext.EmptyRequest{})
		if err != nil {
			client.Close()
			logger.Debug("Failed to resolve azd deployment context")
			return nil, exterrors.FromError(err)
		}
		tenantID = deployment.GetAzureContext().GetScope().GetTenantId()
		if tenantID == "" {
			client.Close()
			return nil, exterrors.MissingTenant()
		}
	}
	credential, err := azdext.NewTokenProvider(ctx, client, &azdext.TokenProviderOptions{TenantID: tenantID})
	if err != nil {
		client.Close()
		logger.Debug("Failed to initialize azd credential")
		return nil, exterrors.FromError(err)
	}
	logger.Debug("Initialized azd runtime")
	return &runtimeContext{Context: ctx, Client: client, Credential: credential, Logger: logger}, nil
}

func (r *runtimeContext) Close() {
	r.Client.Close()
}
