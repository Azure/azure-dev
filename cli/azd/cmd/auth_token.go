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
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type authTokenFlags struct {
	outputFormat string
	scopes       []string
	global       *internal.GlobalCommandOptions
}

func authTokenCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *authTokenFlags) {
	cmd := &cobra.Command{
		Use:    "token",
		Hidden: true,
	}

	getAccessTokenFlags := &authTokenFlags{}
	getAccessTokenFlags.Bind(cmd.Flags(), global)
	return cmd, getAccessTokenFlags
}

func (f *authTokenFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
	output.AddOutputFlag(local, &f.outputFormat, []output.Format{output.JsonFormat}, output.NoneFormat)
	local.StringArrayVar(&f.scopes, "scope", nil, "The scope to use when requesting an access token")
}

type authTokenAction struct {
	credential azcore.TokenCredential
	formatter  output.Formatter
	writer     io.Writer
	flags      authTokenFlags
}

func newAuthTokenAction(
	credential azcore.TokenCredential,
	formatter output.Formatter,
	writer io.Writer,
	flags authTokenFlags,
) *authTokenAction {
	return &authTokenAction{
		credential: credential,
		formatter:  formatter,
		writer:     writer,
		flags:      flags,
	}
}

func (a *authTokenAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(a.flags.scopes) == 0 {
		a.flags.scopes = []string{azure.ManagementScope}
	}

	token, err := a.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: a.flags.scopes,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching token: %w", err)
	}

	res := contracts.AuthTokenResult{
		Token:     token.Token,
		ExpiresOn: token.ExpiresOn,
	}

	return nil, a.formatter.Format(res, a.writer, nil)
}
