// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type authTokenFlags struct {
	tenantID string
	scopes   []string
	global   *internal.GlobalCommandOptions
}

func newAuthTokenFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *authTokenFlags {
	flags := &authTokenFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newAuthTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "token --output json",
		Hidden: true,
	}
}

func (f *authTokenFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	local.StringArrayVar(&f.scopes, "scope", nil, "The scope to use when requesting an access token")
	local.StringVar(&f.tenantID, "tenant-id", "", "The tenant id to use when requesting an access token.")
}

type CredentialProviderFn func(context.Context, *auth.CredentialForCurrentUserOptions) (azcore.TokenCredential, error)

type authTokenAction struct {
	credentialProvider CredentialProviderFn
	formatter          output.Formatter
	writer             io.Writer
	flags              *authTokenFlags
}

func newAuthTokenAction(
	credentialProvider CredentialProviderFn,
	formatter output.Formatter,
	writer io.Writer,
	flags *authTokenFlags,
) actions.Action {
	return &authTokenAction{
		credentialProvider: credentialProvider,
		formatter:          formatter,
		writer:             writer,
		flags:              flags,
	}
}

func (a *authTokenAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(a.flags.scopes) == 0 {
		a.flags.scopes = []string{azure.ManagementScope}
	}

	var cred azcore.TokenCredential

	cred, err := a.credentialProvider(ctx, &auth.CredentialForCurrentUserOptions{
		TenantID: a.flags.tenantID,
	})
	if err != nil {
		return nil, err
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: a.flags.scopes,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching token: %w", err)
	}

	res := contracts.AuthTokenResult{
		Token:     token.Token,
		ExpiresOn: contracts.RFC3339Time(token.ExpiresOn),
	}

	return nil, a.formatter.Format(res, a.writer, nil)
}
