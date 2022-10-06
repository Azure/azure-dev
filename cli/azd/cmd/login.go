// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
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

func (lf *loginFlags) Setup(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&lf.onlyCheckStatus, "check-status", false, "Checks the log-in status instead of logging in.")
	local.BoolVar(&lf.useDeviceCode, "use-device-code", false, "When true, log in by using a device code instead of a browser.")
	output.AddOutputFlag(
		local,
		&lf.outputFormat,
		[]output.Format{output.JsonFormat, output.TableFormat},
		output.TableFormat,
	)

	lf.global = global
}

func loginCmdDesign(global *internal.GlobalCommandOptions) (*cobra.Command, *loginFlags) {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Azure.",
	}

	flags := &loginFlags{}
	flags.Setup(cmd.Flags(), global)
	return cmd, flags
}

type loginAction struct {
	formatter output.Formatter
	writer    io.Writer
	azCli     azcli.AzCli
	flags     loginFlags
}

func newLoginAction(formatter output.Formatter, writer io.Writer, azcli azcli.AzCli, flags loginFlags) *loginAction {
	return &loginAction{
		formatter: formatter,
		writer:    writer,
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

	token, err := la.azCli.GetAccessToken(ctx)
	if errors.Is(err, azcli.ErrAzCliNotLoggedIn) {
		return azcli.ErrAzCliNotLoggedIn
	} else if err != nil {
		return fmt.Errorf("checking auth status: %w", err)
	}

	if la.formatter.Kind() == output.TableFormat {
		fmt.Println("Logged in to Azure.")
		return nil
	}

	var res struct {
		Status    string     `json:"status"`
		ExpiresOn *time.Time `json:"expiresOn"`
	}

	res.Status = "success"
	res.ExpiresOn = token.ExpiresOn

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
	azCli := azcli.GetAzCli(ctx)
	useDeviceCode := forceDeviceCode || os.Getenv(CodespacesEnvVarName) == "true" || os.Getenv(RemoteContainersEnvVarName) == "true"

	return azCli.Login(ctx, useDeviceCode, os.Stdout)
}
