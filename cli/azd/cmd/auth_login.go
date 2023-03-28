// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The parent of the login command.
const loginCmdParentAnnotation = "loginCmdParent"

type authLoginFlags struct {
	loginFlags
}

func newAuthLoginFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *authLoginFlags {
	flags := &authLoginFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

type loginFlags struct {
	onlyCheckStatus        bool
	useDeviceCode          bool
	tenantID               string
	clientID               string
	clientSecret           stringPtr
	clientCertificate      string
	federatedToken         stringPtr
	federatedTokenProvider string
	redirectPort           int
	global                 *internal.GlobalCommandOptions
}

// stringPtr implements a pflag.Value and allows us to distinguish between a flag value being explicitly set to the empty
// string vs not being present.
type stringPtr struct {
	ptr *string
}

func (p *stringPtr) Set(s string) error {
	p.ptr = &s
	return nil
}

func (p *stringPtr) String() string {
	if p.ptr != nil {
		return *p.ptr
	}

	return ""
}

func (p *stringPtr) Type() string {
	return "string"
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
		// For Codespaces in VSCode Browser, interactive browser login will 404 when attempting to redirect to localhost
		// (since azd cannot launch a localhost server when running remotely).
		// Hence, we default to device-code. See https://github.com/Azure/azure-dev/issues/1006
		os.Getenv("CODESPACES") == "true",
		"When true, log in by using a device code instead of a browser.",
	)
	local.StringVar(&lf.clientID, "client-id", "", "The client id for the service principal to authenticate with.")
	local.Var(
		&lf.clientSecret,
		cClientSecretFlagName,
		"The client secret for the service principal to authenticate with. "+
			"Set to the empty string to read the value from the console.")
	local.StringVar(
		&lf.clientCertificate,
		cClientCertificateFlagName,
		"",
		"The path to the client certificate for the service principal to authenticate with.")
	local.Var(
		&lf.federatedToken,
		cFederatedCredentialFlagName,
		"The federated token for the service principal to authenticate with. "+
			"Set to the empty string to read the value from the console.")
	local.StringVar(
		&lf.federatedTokenProvider,
		cFederatedCredentialProviderFlagName,
		"",
		"The provider to use to acquire a federated token to authenticate with.")
	local.StringVar(
		&lf.tenantID,
		"tenant-id",
		"",
		"The tenant id or domain name to authenticate with.")
	local.IntVar(
		&lf.redirectPort,
		"redirect-port",
		0,
		"Choose the port to be used as part of the redirect URI during interactive login.")

	lf.global = global
}

func newLoginFlags(cmd *cobra.Command, global *internal.GlobalCommandOptions) *loginFlags {
	flags := &loginFlags{}
	flags.Bind(cmd.Flags(), global)

	return flags
}

func newLoginCmd(parent string) *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Log in to Azure.",
		Long: heredoc.Doc(`
		Log in to Azure.

		When run without any arguments, log in interactively using a browser. To log in using a device code, pass
		--use-device-code.
		
		To log in as a service principal, pass --client-id and --tenant-id as well as one of: --client-secret, 
		--client-certificate, --federated-credential, or --federated-credential-provider.
		`),
		Annotations: map[string]string{
			loginCmdParentAnnotation: parent,
		},
	}
}

type loginAction struct {
	formatter         output.Formatter
	writer            io.Writer
	console           input.Console
	authManager       *auth.Manager
	accountSubManager *account.SubscriptionsManager
	flags             *loginFlags
	annotations       CmdAnnotations
}

func newAuthLoginAction(
	formatter output.Formatter,
	writer io.Writer,
	authManager *auth.Manager,
	accountSubManager *account.SubscriptionsManager,
	flags *authLoginFlags,
	console input.Console,
	annotations CmdAnnotations,
) actions.Action {
	return &loginAction{
		formatter:         formatter,
		writer:            writer,
		console:           console,
		authManager:       authManager,
		accountSubManager: accountSubManager,
		flags:             &flags.loginFlags,
		annotations:       annotations,
	}
}

func newLoginAction(
	formatter output.Formatter,
	writer io.Writer,
	authManager *auth.Manager,
	accountSubManager *account.SubscriptionsManager,
	flags *loginFlags,
	console input.Console,
	annotations CmdAnnotations,
) actions.Action {
	return &loginAction{
		formatter:         formatter,
		writer:            writer,
		console:           console,
		authManager:       authManager,
		accountSubManager: accountSubManager,
		flags:             flags,
		annotations:       annotations,
	}
}

func (la *loginAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if la.annotations[loginCmdParentAnnotation] == "" {
		fmt.Fprintln(
			la.console.Handles().Stderr,
			//nolint:lll
			output.WithWarningFormat(
				"WARNING: `azd login` has been deprecated and will be removed in a future release. Please use `azd auth login` instead."),
		)
	}

	if !la.flags.onlyCheckStatus {
		if err := la.accountSubManager.ClearSubscriptions(ctx); err != nil {
			log.Printf("failed clearing subscriptions: %v", err)
		}

		if err := la.login(ctx); err != nil {
			return nil, err
		}
	}

	res := contracts.LoginResult{}

	credOptions := auth.CredentialForCurrentUserOptions{
		TenantID: la.flags.tenantID,
	}

	if cred, err := la.authManager.CredentialForCurrentUser(ctx, &credOptions); errors.Is(err, auth.ErrNoCurrentUser) {
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

	if !la.flags.onlyCheckStatus && res.Status == contracts.LoginStatusSuccess {
		// Rehydrate or clear the account's subscriptions cache.
		// The caching is done here to increase responsiveness of listing subscriptions (during azd init).
		// It also allows an implicit command for the user to refresh cached subscriptions.
		if la.flags.clientID == "" {
			// Deleting subscriptions on file is very unlikely to fail, unless there are serious filesystem issues.
			// If this does fail, we want the user to be aware of this. Like other stored azd account data,
			// stored subscriptions are currently tied to the OS user, and not the individual account being logged in,
			// this means cross-contamination is possible.
			if err := la.accountSubManager.ClearSubscriptions(ctx); err != nil {
				return nil, err
			}

			err := la.accountSubManager.RefreshSubscriptions(ctx)
			if err != nil {
				// If this fails, the subscriptions will still be loaded on-demand.
				// erroring out when the user interacts with subscriptions is much more user-friendly.
				log.Printf("failed retrieving subscriptions: %v", err)
			}
		} else {
			// Service principals do not typically require subscription caching (running in CI scenarios)
			// We simply clear the cache, which is much faster than rehydrating.
			err := la.accountSubManager.ClearSubscriptions(ctx)
			if err != nil {
				log.Printf("failed clearing subscriptions: %v", err)
			}
		}
	}

	if la.formatter.Kind() == output.NoneFormat {
		if res.Status == contracts.LoginStatusSuccess {
			fmt.Fprintln(la.console.Handles().Stdout, "Logged in to Azure.")
		} else {
			fmt.Fprintln(la.console.Handles().Stdout, "Not logged in, run `azd auth login` to login to Azure.")
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
	if la.flags.clientID != "" {
		if la.flags.tenantID == "" {
			return errors.New("must set both `client-id` and `tenant-id` for service principal login")
		}

		if countTrue(
			la.flags.clientSecret.ptr != nil,
			la.flags.clientCertificate != "",
			la.flags.federatedToken.ptr != nil,
			la.flags.federatedTokenProvider != "",
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
		case la.flags.clientSecret.ptr != nil:
			if *la.flags.clientSecret.ptr == "" {
				v, err := la.console.Prompt(ctx, input.ConsoleOptions{
					Message: "Enter your client secret",
				})
				if err != nil {
					return fmt.Errorf("prompting for client secret: %w", err)
				}
				la.flags.clientSecret.ptr = &v
			}

			if _, err := la.authManager.LoginWithServicePrincipalSecret(
				ctx, la.flags.tenantID, la.flags.clientID, *la.flags.clientSecret.ptr,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}
		case la.flags.clientCertificate != "":
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
		case la.flags.federatedToken.ptr != nil:
			if *la.flags.federatedToken.ptr == "" {
				v, err := la.console.Prompt(ctx, input.ConsoleOptions{
					Message: "Enter your federated token",
				})
				if err != nil {
					return fmt.Errorf("prompting for federated token: %w", err)
				}
				la.flags.federatedToken.ptr = &v
			}

			if _, err := la.authManager.LoginWithServicePrincipalFederatedToken(
				ctx, la.flags.tenantID, la.flags.clientID, *la.flags.federatedToken.ptr,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}
		case la.flags.federatedTokenProvider != "":
			if _, err := la.authManager.LoginWithServicePrincipalFederatedTokenProvider(
				ctx, la.flags.tenantID, la.flags.clientID, la.flags.federatedTokenProvider,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}
		}

		return nil
	}

	if la.flags.useDeviceCode {
		if _, err := la.authManager.LoginWithDeviceCode(ctx, la.writer, la.flags.tenantID); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	} else {
		if _, err := la.authManager.LoginInteractive(ctx, la.flags.redirectPort, la.flags.tenantID); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	}

	return nil
}
