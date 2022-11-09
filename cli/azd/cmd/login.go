// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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
	// This is used to detect cases where the flag was explicitly passed but the value was the empty string. We use this
	// to allow setting an empty value for an flag to denote its value should be read from the console.
	flagPassedFn           func(string) bool
	onlyCheckStatus        bool
	useDeviceCode          bool
	outputFormat           string
	tenantID               string
	clientID               string
	clientSecret           string
	clientCertificate      string
	federatedToken         string
	federatedTokenProvider string
	global                 *internal.GlobalCommandOptions
}

const (
	cClientSecretFlagName                = "client-secret"
	cClientCertificateFlagName           = "client-certificate"
	cFederatedCredentialFlagName         = "federated-credential"
	cFederatedCredentialProviderFlagName = "federated-credential-provider"
)

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
		cClientSecretFlagName,
		"",
		"The client secret for the service principal to authenticate with. "+
			"Set to the empty string to read the value from the console.")
	local.StringVar(
		&lf.clientCertificate,
		cClientCertificateFlagName,
		"",
		"The path to the client certificate for the service principal to authenticate with.")
	local.StringVar(
		&lf.federatedToken,
		cFederatedCredentialFlagName,
		"",
		"The federated token for the service principal to authenticate with. "+
			"Set to the empty string to read the value from the console.")
	local.StringVar(
		&lf.federatedTokenProvider,
		cFederatedCredentialProviderFlagName,
		"",
		"The provider to use to acquire a federated token to authenticate with.")
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

	flags := &loginFlags{
		flagPassedFn: func(name string) bool {
			return cmd.Flags().Lookup(name).Changed
		},
	}
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

func countTrue(elms ...bool) int {
	i := 0

	for _, elm := range elms {
		if elm {
			i++
		}
	}

	return i
}

func (la *loginAction) login(ctx context.Context) error {
	if la.flags.clientID != "" || la.flags.tenantID != "" {
		if la.flags.clientID == "" || la.flags.tenantID == "" {
			return errors.New("must set both `client-id` and `tenant-id` for service principal login")
		}

		if countTrue(
			la.flags.flagPassedFn(cClientSecretFlagName),
			la.flags.flagPassedFn(cClientCertificateFlagName),
			la.flags.flagPassedFn(cFederatedCredentialFlagName),
			la.flags.flagPassedFn(cFederatedCredentialProviderFlagName),
		) != 1 {
			return fmt.Errorf(
				"must set exactly one of %s for service principal", strings.Join([]string{
					cClientSecretFlagName,
					cClientCertificateFlagName,
					cFederatedCredentialFlagName,
					cFederatedCredentialProviderFlagName,
				}, ", "))
		}

		switch {
		case la.flags.flagPassedFn(cClientSecretFlagName):
			if la.flags.clientSecret == "" {
				v, err := la.console.Prompt(ctx, input.ConsoleOptions{
					Message: "Enter your client secret",
				})
				if err != nil {
					return fmt.Errorf("prompting for client secret: %w", err)
				}
				la.flags.clientSecret = v
			}

			if _, err := la.authManager.LoginWithServicePrincipalSecret(
				ctx, la.flags.tenantID, la.flags.clientID, la.flags.clientSecret,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}
		case la.flags.flagPassedFn(cClientCertificateFlagName):
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
		case la.flags.flagPassedFn(cFederatedCredentialFlagName):
			if la.flags.clientSecret == "" {
				v, err := la.console.Prompt(ctx, input.ConsoleOptions{
					Message: "Enter your federated token",
				})
				if err != nil {
					return fmt.Errorf("prompting for federated token: %w", err)
				}
				la.flags.clientSecret = v
			}

			if _, err := la.authManager.LoginWithServicePrincipalFederatedToken(
				ctx, la.flags.tenantID, la.flags.clientID, la.flags.federatedToken,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}
		case la.flags.flagPassedFn(cFederatedCredentialProviderFlagName):
			if _, err := la.authManager.LoginWithServicePrincipalFederatedTokenProvider(
				ctx, la.flags.tenantID, la.flags.clientID, la.flags.federatedTokenProvider,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}
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
