// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
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
	formatter   output.Formatter
	writer      io.Writer
	console     input.Console
	authManager auth.Manager
	flags       loginFlags
}

func newLoginAction(
	formatter output.Formatter,
	writer io.Writer,
	authManager auth.Manager,
	flags loginFlags,
	console input.Console,
) *loginAction {
	return &loginAction{
		formatter:   formatter,
		writer:      writer,
		console:     console,
		authManager: authManager,
		flags:       flags,
	}
}

const (
	// CodespacesEnvVarName is the name of the env variable set when you're in a Github codespace. It's
	// just set to 'true'.
	CodespacesEnvVarName = "CODESPACES"

	// RemoteContainersEnvVarName is the name of the env variable set when you're in a remote container. It's
	// just set to 'true'.
	RemoteContainersEnvVarName = "REMOTE_CONTAINERS"
)

func (la *loginAction) Run(ctx context.Context) error {
	if !la.flags.onlyCheckStatus {
		useDeviceCode := la.flags.useDeviceCode || os.Getenv(CodespacesEnvVarName) == "true" ||
			os.Getenv(RemoteContainersEnvVarName) == "true"

		_, _, err := la.authManager.Login(ctx, useDeviceCode)
		if err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	}

	res := contracts.LoginResult{}

	if _, cred, err := la.authManager.CurrentAccount(ctx); errors.Is(err, auth.ErrNoCurrentUser) {
		res.Status = contracts.LoginStatusUnauthenticated
	} else if err != nil {
		return fmt.Errorf("checking auth status: %w", err)
	} else {
		if token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
			Scopes: auth.LoginScopes,
		}); err != nil {
			res.Status = contracts.LoginStatusUnauthenticated
		} else {
			res.Status = contracts.LoginStatusSuccess
			res.ExpiresOn = &token.ExpiresOn
		}
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
