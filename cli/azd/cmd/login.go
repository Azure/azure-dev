// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type loginFlags struct {
	onlyCheckStatus   bool
	useDeviceCode     bool
	outputFormat      string
	tenantID          string
	clientID          string
	clientSecret      string
	clientCertificate string
	federatedToken    string
	global            *internal.GlobalCommandOptions
}

func (lf *loginFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&lf.onlyCheckStatus, "check-status", false, "Checks the log-in status instead of logging in.")
	local.BoolVar(
		&lf.useDeviceCode,
		"use-device-code",
		false,
		"When true, log in by using a device code instead of a browser.",
	)
	local.StringVar(&lf.clientID, "client-id", "", "The client id for the service principal to authenticate with.")
	local.StringVar(
		&lf.clientSecret,
		"client-secret",
		"",
		"The client secret for the service principal to authenticate with.")
	local.StringVar(
		&lf.clientCertificate,
		"client-certificate",
		"",
		"The path to the client certificate for the service principal to authenticate with.")
	local.StringVar(
		&lf.federatedToken,
		"federated-token",
		"",
		"The federated token for the service principal to authenticate with.")
	local.StringVar(&lf.tenantID, "tenant-id", "", "The tenant id for the service principal to authenticate with.")
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
		Annotations: map[string]string{
			commands.RequireNoLoginAnnotation: "true",
		},
	}

	flags := &loginFlags{}
	flags.Bind(cmd.Flags(), global)
	return cmd, flags
}

type loginAction struct {
	formatter   output.Formatter
	writer      io.Writer
	console     input.Console
	authManager *auth.Manager
	flags       loginFlags
}

func newLoginAction(
	formatter output.Formatter,
	writer io.Writer,
	authManager *auth.Manager,
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

func (la *loginAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if !la.flags.onlyCheckStatus {
		if err := la.login(ctx); err != nil {
			return nil, err
		}
	}

	res := contracts.LoginResult{}

	if cred, err := la.authManager.CredentialForCurrentUser(ctx); errors.Is(err, auth.ErrNoCurrentUser) {
		res.Status = contracts.LoginStatusUnauthenticated
	} else if err != nil {
		return nil, fmt.Errorf("checking auth status: %w", err)
	} else {
		if token, err := auth.EnsureLoggedInCredential(ctx, cred); errors.Is(err, auth.ErrNoCurrentUser) {
			res.Status = contracts.LoginStatusUnauthenticated
		} else if err != nil {
			return nil, fmt.Errorf("checking auth status: %w", err)
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

		return nil, nil
	}

	return nil, la.formatter.Format(res, la.writer, nil)
}

func (la *loginAction) login(ctx context.Context) error {
	if la.flags.clientID != "" || la.flags.tenantID != "" {
		if la.flags.clientID == "" || la.flags.tenantID == "" {
			return errors.New("must set both `client-id` and `tenant-id` for service principal login")
		}

		switch {
		// only --client-secret was passed
		case la.flags.clientSecret != "" && la.flags.clientCertificate == "" && la.flags.federatedToken == "":
			if _, err := la.authManager.LoginWithServicePrincipalSecret(
				ctx, la.flags.tenantID, la.flags.clientID, la.flags.clientSecret,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}

		// only --client-certificate was passed
		case la.flags.clientSecret != "" && la.flags.clientCertificate != "" && la.flags.federatedToken == "":
			certFile, err := os.Open(la.flags.clientCertificate)
			if err != nil {
				return fmt.Errorf("reading certificate: %w", err)
			}
			defer certFile.Close()

			cert, err := io.ReadAll(certFile)
			if err != nil {
				return fmt.Errorf("reading certificate: %w", err)
			}

			if _, err := la.authManager.LoginWithServicePrincipalCertificate(
				ctx, la.flags.tenantID, la.flags.clientID, cert,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}

		// only --federated-token was passed
		case la.flags.clientSecret != "" && la.flags.clientCertificate == "" && la.flags.federatedToken != "":
			if _, err := la.authManager.LoginWithServicePrincipalFederatedToken(
				ctx, la.flags.tenantID, la.flags.clientID, la.flags.federatedToken,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}

		// some other combination was set.
		default:
			return errors.New(
				"must set exactly one of `client-secret`, `client-certificate` or `federated-token` for service principal")
		}

		return nil
	}

	useDeviceCode := la.flags.useDeviceCode || os.Getenv(CodespacesEnvVarName) == "true" ||
		os.Getenv(RemoteContainersEnvVarName) == "true"

	if useDeviceCode {
		if _, err := la.authManager.LoginWithDeviceCode(ctx, la.writer); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	} else {
		if _, err := la.authManager.LoginInteractive(ctx); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	}

	return nil
}
