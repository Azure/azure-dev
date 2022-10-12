// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/cmd/contracts"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func loginCmd(rootOptions *internal.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&loginAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"login",
		"Log in to Azure.",
		nil,
	)

	return output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)
}

type loginAction struct {
	rootOptions     *internal.GlobalCommandOptions
	onlyCheckStatus bool
	useDeviceCode   bool
}

var _ commands.Action = &loginAction{}

func (la *loginAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	formatter := output.GetFormatter(ctx)
	writer := output.GetWriter(ctx)

	azCli := azcli.GetAzCli(ctx)
	if err := tools.EnsureInstalled(ctx, azCli); err != nil {
		return err
	}

	if !la.onlyCheckStatus {
		if err := runLogin(ctx, la.useDeviceCode); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	}

	var res contracts.LoginResult

	if token, err := azCli.GetAccessToken(ctx); errors.Is(err, azcli.ErrAzCliNotLoggedIn) || errors.Is(err, azcli.ErrAzCliRefreshTokenExpired) {
		res.Status = contracts.LoginStatusUnauthenticated
	} else if err != nil {
		return fmt.Errorf("checking auth status: %w", err)
	} else {
		res.Status = contracts.LoginStatusSuccess
		res.ExpiresOn = token.ExpiresOn
	}

	if formatter.Kind() == output.NoneFormat {
		if res.Status == contracts.LoginStatusSuccess {
			fmt.Println("Logged in to Azure.")
		} else {
			fmt.Println("Not logged in, run `azd login` to login to Azure.")
		}

		return nil
	}

	return formatter.Format(res, writer, nil)
}

func (la *loginAction) SetupFlags(persistent *pflag.FlagSet, local *pflag.FlagSet) {
	local.BoolVar(&la.onlyCheckStatus, "check-status", false, "Checks the log-in status instead of logging in.")
	local.BoolVar(&la.useDeviceCode, "use-device-code", false, "When true, log in by using a device code instead of a browser.")
}

// ensureLoggedIn checks to see if the user is currently logged in. If not, the equivalent of `az login` is run.
func ensureLoggedIn(ctx context.Context) error {
	azCli := azcli.GetAzCli(ctx)
	_, err := azCli.GetAccessToken(ctx)
	if errors.Is(err, azcli.ErrAzCliNotLoggedIn) || errors.Is(err, azcli.ErrAzCliRefreshTokenExpired) {
		if err := runLogin(ctx, false); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("fetching access token: %w", err)
	}

	return nil
}

// runLogin runs an interactive login. When running in a Codespace or Remote Container, a device code based is
// preformed since the default browser login needs UI. A device code login can be forced with `forceDeviceCode`.
func runLogin(ctx context.Context, forceDeviceCode bool) error {
	const (
		// CodespacesEnvVarName is the name of the env variable set when you're in a Github codespace. It's
		// just set to 'true'.
		CodespacesEnvVarName = "CODESPACES"

		// RemoteContainersEnvVarName is the name of the env variable set when you're in a remote container. It's
		// just set to 'true'.
		RemoteContainersEnvVarName = "REMOTE_CONTAINERS"
	)

	azCli := azcli.GetAzCli(ctx)
	useDeviceCode := forceDeviceCode || os.Getenv(CodespacesEnvVarName) == "true" || os.Getenv(RemoteContainersEnvVarName) == "true"

	return azCli.Login(ctx, useDeviceCode, os.Stdout)
}
