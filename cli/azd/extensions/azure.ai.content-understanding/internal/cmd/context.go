// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"os"
	"strings"

	"azure.ai.content-understanding/internal/config"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type runtimeContext struct {
	Context    context.Context
	Client     *azdext.AzdClient
	Credential *azdext.TokenProvider
	Logger     *azdext.Logger
	Config     config.Resolved
}

func newRuntimeContext(
	cmd *cobra.Command, extCtx *azdext.ExtensionContext, options config.Options,
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
	resolved, err := config.Load(ctx, client, extCtx.Environment, options, config.ProcessValues())
	if err != nil {
		client.Close()
		logger.Debug("Failed to resolve CU configuration")
		return nil, exterrors.FromError(err)
	}
	credential, err := azdext.NewTokenProvider(ctx, client, &azdext.TokenProviderOptions{TenantID: resolved.TenantID})
	if err != nil {
		client.Close()
		logger.Debug("Failed to initialize azd credential")
		return nil, exterrors.FromError(err)
	}
	logger.Debug("Initialized azd runtime")
	return &runtimeContext{
		Context: ctx, Client: client, Credential: credential, Logger: logger, Config: resolved,
	}, nil
}

func (r *runtimeContext) Close() {
	r.Client.Close()
}
