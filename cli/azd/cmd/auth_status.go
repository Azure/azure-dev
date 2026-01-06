// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type authStatusFlags struct {
	global *internal.GlobalCommandOptions
}

func newAuthStatusFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *authStatusFlags {
	flags := &authStatusFlags{}
	flags.Bind(cmd.Flags(), global)
	return flags
}

func (f *authStatusFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	f.global = global
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current authentication status.",
		Long:  "Display whether you are logged in to Azure and the associated account information.",
	}
}

type authStatusAction struct {
	formatter   output.Formatter
	writer      io.Writer
	console     input.Console
	authManager *auth.Manager
	flags       *authStatusFlags
}

func newAuthStatusAction(
	formatter output.Formatter,
	writer io.Writer,
	authManager *auth.Manager,
	flags *authStatusFlags,
	console input.Console,
) actions.Action {
	return &authStatusAction{
		formatter:   formatter,
		writer:      writer,
		console:     console,
		authManager: authManager,
		flags:       flags,
	}
}

func (a *authStatusAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	scopes := a.authManager.LoginScopes()

	// get user account information
	details, err := a.authManager.LogInDetails(ctx)
	var loginExpiryError *auth.ReLoginRequiredError
	if err != nil {
		if !errors.Is(err, auth.ErrNoCurrentUser) &&
			!errors.As(err, &loginExpiryError) {
			// print a useful message for unknown errors
			fmt.Fprintln(a.console.Handles().Stderr, err.Error())
		}
		log.Printf("error: getting signed in account: %v", err)
	}

	res := contracts.StatusResult{}
	if err != nil {
		res.Status = contracts.AuthStatusUnauthenticated
	} else {
		res.Status = contracts.AuthStatusAuthenticated
		_, err := a.verifyLoggedIn(ctx, scopes)
		if err != nil {
			res.Status = contracts.AuthStatusUnauthenticated
			log.Printf("error: verifying logged in status: %v", err)
		}

		switch details.LoginType {
		case auth.EmailLoginType:
			res.Type = contracts.AccountTypeUser
			res.Email = details.Account
		case auth.ClientIdLoginType:
			res.Type = contracts.AccountTypeServicePrincipal
			res.ClientID = details.Account
		}
	}

	if a.formatter.Kind() != output.NoneFormat {
		a.formatter.Format(res, a.writer, nil)
		return nil, nil
	}

	a.console.MessageUxItem(ctx, &ux.AuthStatusView{Result: &res})
	return nil, nil
}

// Verifies that the user has credentials stored,
// and that the credentials stored is accepted by the identity server (can be exchanged for access token).
func (a *authStatusAction) verifyLoggedIn(ctx context.Context, scopes []string) (*azcore.AccessToken, error) {
	cred, err := a.authManager.CredentialForCurrentUser(ctx, nil)
	if err != nil {
		return nil, err
	}

	// Ensure credential is valid, and can be exchanged for an access token
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: scopes,
	})

	if err != nil {
		return nil, err
	}

	return &token, nil
}
