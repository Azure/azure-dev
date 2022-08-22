// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func loginCmd(rootOptions *commands.GlobalCommandOptions) *cobra.Command {
	cmd := commands.Build(
		&loginAction{
			rootOptions: rootOptions,
		},
		rootOptions,
		"login",
		"Log in to Azure.",
		"",
	)

	return output.AddOutputParam(
		cmd,
		[]output.Format{output.JsonFormat, output.TableFormat},
		output.TableFormat,
	)
}

type loginAction struct {
	rootOptions     *commands.GlobalCommandOptions
	onlyCheckStatus bool
	useDeviceCode   bool
}

var _ commands.Action = &loginAction{}

func (la *loginAction) Run(ctx context.Context, cmd *cobra.Command, args []string, azdCtx *azdcontext.AzdContext) error {
	formatter, err := output.GetFormatter(cmd)
	if err != nil {
		return err
	}

	azCli := commands.GetAzCliFromContext(ctx)
	if err := tools.EnsureInstalled(ctx, azCli); err != nil {
		return err
	}

	if !la.onlyCheckStatus {
		if err := runLogin(ctx, la.useDeviceCode); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	}

	token, err := azCli.GetAccessToken(ctx)
	if errors.Is(err, azcli.ErrAzCliNotLoggedIn) {
		return azcli.ErrAzCliNotLoggedIn
	} else if err != nil {
		return fmt.Errorf("checking auth status: %w", err)
	}

	if formatter.Kind() == output.TableFormat {
		fmt.Println("Logged in to Azure.")
		return nil
	}

	var res struct {
		Status    string     `json:"status"`
		ExpiresOn *time.Time `json:"expiresOn"`
	}

	res.Status = "success"
	res.ExpiresOn = token.ExpiresOn

	return formatter.Format(res, cmd.OutOrStdout(), nil)
}

func (la *loginAction) SetupFlags(persistent *pflag.FlagSet, local *pflag.FlagSet) {
	local.BoolVar(&la.onlyCheckStatus, "check-status", false, "Checks the log-in status instead of logging in.")
	local.BoolVar(&la.useDeviceCode, "use-device-code", false, "When true, log in by using a device code instead of a browser.")
}

// ensureLoggedIn checks to see if the user is currently logged in. If not, the equivalent of `az login` is run.
func ensureLoggedIn(ctx context.Context) error {
	azCli := commands.GetAzCliFromContext(ctx)
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
	azCli := commands.GetAzCliFromContext(ctx)
	useDeviceCode := forceDeviceCode || os.Getenv(CodespacesEnvVarName) == "true" || os.Getenv(RemoteContainersEnvVarName) == "true"

	return azCli.Login(ctx, useDeviceCode, os.Stdout)
}
