// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type loginFlags struct {
	onlyCheckStatus bool
	useDeviceCode   bool
	outputFormat    string
	global          *internal.GlobalCommandOptions
}

func (lf *loginFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&lf.onlyCheckStatus, "check-status", false, "Checks the log-in status instead of logging in.")
	local.BoolVar(
		&lf.useDeviceCode,
		"use-device-code",
		false,
		"When true, log in by using a device code instead of a browser.",
	)
	output.AddOutputFlag(
		local,
		&lf.outputFormat,
		[]output.Format{output.JsonFormat, output.NoneFormat},
		output.NoneFormat,
	)

	lf.global = global
}

func loginCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *loginFlags) {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Azure.",
	}

	flags := &loginFlags{}
	flags.Bind(cmd.Flags(), global)
	return cmd, flags
}

type loginAction struct {
	formatter output.Formatter
	writer    io.Writer
	console   input.Console
	azCli     azcli.AzCli
	flags     loginFlags
}

func newLoginAction(
	formatter output.Formatter,
	writer io.Writer,
	azcli azcli.AzCli,
	flags loginFlags,
	console input.Console,
) *loginAction {
	return &loginAction{
		formatter: formatter,
		writer:    writer,
		console:   console,
		azCli:     azcli,
		flags:     flags,
	}
}

func (la *loginAction) Run(ctx context.Context) error {
	if err := tools.EnsureInstalled(ctx, la.azCli); err != nil {
		return err
	}

	if !la.flags.onlyCheckStatus {
		if err := runLogin(ctx, la.flags.useDeviceCode); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	}

	res := contracts.LoginResult{}

	if token, err := la.azCli.GetAccessToken(ctx); errors.Is(err, azcli.ErrAzCliNotLoggedIn) ||
		errors.Is(err, azcli.ErrAzCliRefreshTokenExpired) {
		res.Status = contracts.LoginStatusUnauthenticated
	} else if err != nil {
		return fmt.Errorf("checking auth status: %w", err)
	} else {
		res.Status = contracts.LoginStatusSuccess
		res.ExpiresOn = token.ExpiresOn
	}

	if la.formatter.Kind() == output.NoneFormat {
		if res.Status == contracts.LoginStatusSuccess {
			fmt.Fprintln(la.console.Handles().Stdout, "Logged in to Azure.")
		} else {
			fmt.Fprintln(la.console.Handles().Stdout, "Not logged in, run `azd login` to login to Azure.")
		}

		return nil
	}

	return la.formatter.Format(res, la.writer, nil)
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
	console := input.GetConsole(ctx)
	if console == nil {
		panic("need console")
	}

	const (
		// CodespacesEnvVarName is the name of the env variable set when you're in a Github codespace. It's
		// just set to 'true'.
		CodespacesEnvVarName = "CODESPACES"

		// RemoteContainersEnvVarName is the name of the env variable set when you're in a remote container. It's
		// just set to 'true'.
		RemoteContainersEnvVarName = "REMOTE_CONTAINERS"
	)

	azCli := azcli.GetAzCli(ctx)
	useDeviceCode := forceDeviceCode || os.Getenv(CodespacesEnvVarName) == "true" ||
		os.Getenv(RemoteContainersEnvVarName) == "true"

	return azCli.Login(ctx, useDeviceCode, console.Handles().Stdout)
}
