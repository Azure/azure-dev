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
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/runcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/oneauth"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The parent of the login command.
const loginCmdParentAnnotation = "loginCmdParent"

// azurePipelinesClientIDEnvVarName is the name of the environment variable that contains the client ID for the principal
// to use when authenticating with Azure Pipelines via OIDC. It is set by both the AzureCLI@2 and AzurePowerShell@5 tasks
// when using a service connection or can be set manually when not using these tasks.
const azurePipelinesClientIDEnvVarName = "AZURESUBSCRIPTION_CLIENT_ID"

// azurePipelinesTenantIDEnvVarName is the name of the environment variable that contains the tenant ID for the principal
// to use when authenticating with Azure Pipelines via OIDC. It is set by both the AzureCLI@2 and AzurePowerShell@5 tasks
// when using a service connection or can be set manually when not using these tasks.
const azurePipelinesTenantIDEnvVarName = "AZURESUBSCRIPTION_TENANT_ID"

// AzurePipelinesServiceConnectionNameEnvVarName is the name of the environment variable that contains the name of the
// service connection to use when authenticating with Azure Pipelines via OIDC. It is set by both the AzureCLI@2 and
// AzurePowerShell@5 tasks when using a service connection or can be set manually when not using these tasks.
const azurePipelinesServiceConnectionIDEnvVarName = "AZURESUBSCRIPTION_SERVICE_CONNECTION_ID"

// azurePipelinesProvider is the name of the federated token provider to use when authenticating with Azure Pipelines via
// OIDC.
const azurePipelinesProvider string = "azure-pipelines"

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
	browser                bool
	managedIdentity        bool
	useDeviceCode          boolPtr
	tenantID               string
	clientID               string
	clientSecret           stringPtr
	clientCertificate      string
	federatedTokenProvider string
	scopes                 []string
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

// boolPtr implements a pflag.Value and allows us to distinguish between a flag value being explicitly set to
// bool vs not being present.
type boolPtr struct {
	ptr *string
}

func (p *boolPtr) Set(s string) error {
	p.ptr = &s
	return nil
}

func (p *boolPtr) String() string {
	if p.ptr != nil {
		return *p.ptr
	}

	return "false"
}

func (p *boolPtr) Type() string {
	return ""
}

const (
	cClientSecretFlagName                = "client-secret"
	cClientCertificateFlagName           = "client-certificate"
	cFederatedCredentialProviderFlagName = "federated-credential-provider"
)

func (lf *loginFlags) Bind(local *pflag.FlagSet, global *internal.GlobalCommandOptions) {
	local.BoolVar(&lf.onlyCheckStatus, "check-status", false, "Checks the log-in status instead of logging in.")
	f := local.VarPF(
		&lf.useDeviceCode,
		"use-device-code",
		"",
		"When true, log in by using a device code instead of a browser.",
	)
	// ensure the flag behaves as a common boolean flag which is set to true when used without any other arg
	f.NoOptDefVal = "true"
	local.BoolVar(
		&lf.managedIdentity,
		"managed-identity",
		false,
		"Use a managed identity to authenticate.",
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
	local.StringArrayVar(
		&lf.scopes,
		"scope",
		nil,
		"The scope to acquire during login")
	_ = local.MarkHidden("scope")
	local.IntVar(
		&lf.redirectPort,
		"redirect-port",
		0,
		"Choose the port to be used as part of the redirect URI during interactive login.")
	if oneauth.Supported {
		local.BoolVar(&lf.browser, "browser", false, "Authenticate in a web browser instead of an integrated dialog.")
	}

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
		--client-certificate, or --federated-credential-provider.

		To log in using a managed identity, pass --managed-identity, which will use the system assigned managed identity.
		To use a user assigned managed identity, pass --client-id in addition to --managed-identity with the client id of
		the user assigned managed identity you wish to use.
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
	commandRunner     exec.CommandRunner
}

// it is important to update both newAuthLoginAction and newLoginAction at the same time
// newAuthLoginAction is the action that is bound to `azd auth login`,
// and newLoginAction is the action that is bound to `azd login`
func newAuthLoginAction(
	formatter output.Formatter,
	writer io.Writer,
	authManager *auth.Manager,
	accountSubManager *account.SubscriptionsManager,
	flags *authLoginFlags,
	console input.Console,
	annotations CmdAnnotations,
	commandRunner exec.CommandRunner,
) actions.Action {
	return &loginAction{
		formatter:         formatter,
		writer:            writer,
		console:           console,
		authManager:       authManager,
		accountSubManager: accountSubManager,
		flags:             &flags.loginFlags,
		annotations:       annotations,
		commandRunner:     commandRunner,
	}
}

// it is important to update both newAuthLoginAction and newLoginAction at the same time
// newAuthLoginAction is the action that is bound to `azd auth login`,
// and newLoginAction is the action that is bound to `azd login`
func newLoginAction(
	formatter output.Formatter,
	writer io.Writer,
	authManager *auth.Manager,
	accountSubManager *account.SubscriptionsManager,
	flags *loginFlags,
	console input.Console,
	annotations CmdAnnotations,
	commandRunner exec.CommandRunner,
) actions.Action {
	return &loginAction{
		formatter:         formatter,
		writer:            writer,
		console:           console,
		authManager:       authManager,
		accountSubManager: accountSubManager,
		flags:             flags,
		annotations:       annotations,
		commandRunner:     commandRunner,
	}
}

func (la *loginAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	if len(la.flags.scopes) == 0 {
		la.flags.scopes = la.authManager.LoginScopes()
	}

	if la.annotations[loginCmdParentAnnotation] == "" {
		fmt.Fprintln(
			la.console.Handles().Stderr,
			output.WithWarningFormat(
				"WARNING: `azd login` is deprecated and will be removed in a future release."))
		fmt.Fprintln(
			la.console.Handles().Stderr,
			"Next time use `azd auth login`.")
	}

	if la.flags.onlyCheckStatus {
		// In check status mode, we always print the final status to stdout.
		// We print any non-setup related errors to stderr.
		// We always return a zero exit code.
		token, err := la.verifyLoggedIn(ctx)
		var loginExpiryError *auth.ReLoginRequiredError
		if err != nil &&
			!errors.Is(err, auth.ErrNoCurrentUser) &&
			!errors.As(err, &loginExpiryError) {
			fmt.Fprintln(la.console.Handles().Stderr, err.Error())
		}

		res := contracts.LoginResult{}
		if err != nil {
			res.Status = contracts.LoginStatusUnauthenticated
		} else {
			res.Status = contracts.LoginStatusSuccess
			res.ExpiresOn = &token.ExpiresOn
		}

		if la.formatter.Kind() != output.NoneFormat {
			return nil, la.formatter.Format(res, la.writer, nil)
		} else {
			var msg string
			switch res.Status {
			case contracts.LoginStatusSuccess:
				msg = "Logged in to Azure."
			case contracts.LoginStatusUnauthenticated:
				msg = "Not logged in, run `azd auth login` to login to Azure."
			default:
				panic("Unhandled login status")
			}

			fmt.Fprintln(la.console.Handles().Stdout, msg)
			return nil, nil
		}
	}

	if err := la.login(ctx); err != nil {
		return nil, err
	}

	if _, err := la.verifyLoggedIn(ctx); err != nil {
		return nil, err
	}

	if la.flags.clientID == "" {
		// Update the subscriptions cache for regular users (i.e. non-service-principals).
		// The caching is done here to increase responsiveness of listing subscriptions in the application.
		// It also allows an implicit command for the user to refresh cached subscriptions.
		if err := la.accountSubManager.RefreshSubscriptions(ctx); err != nil {
			// If this fails, the subscriptions will still be loaded on-demand.
			// erroring out when the user interacts with subscriptions is much more user-friendly.
			log.Printf("failed retrieving subscriptions: %v", err)
		}
	}

	la.console.Message(ctx, "Logged in to Azure.")
	return nil, nil
}

// Verifies that the user has credentials stored,
// and that the credentials stored is accepted by the identity server (can be exchanged for access token).
func (la *loginAction) verifyLoggedIn(ctx context.Context) (*azcore.AccessToken, error) {
	credOptions := auth.CredentialForCurrentUserOptions{
		TenantID: la.flags.tenantID,
	}

	cred, err := la.authManager.CredentialForCurrentUser(ctx, &credOptions)
	if err != nil {
		return nil, err
	}

	// Ensure credential is valid, and can be exchanged for an access token
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: la.flags.scopes,
	})

	if err != nil {
		return nil, err
	}

	return &token, nil
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

// runningOnCodespacesBrowser use `code --status` which returns:
//
//	> The --status argument is not yet supported in browsers.
//
// to detect when vscode is within a WebBrowser environment.
func runningOnCodespacesBrowser(ctx context.Context, commandRunner exec.CommandRunner) bool {
	runArgs := exec.NewRunArgs("code", "--status")
	result, err := commandRunner.Run(ctx, runArgs)
	if err != nil {
		// An error here means VSCode is not installed or found, or something else.
		// At any case, we know VSCode is not within a webBrowser
		log.Printf("error running code --status: %s", err.Error())
		return false
	}

	return strings.Contains(result.Stdout, "The --status argument is not yet supported in browsers")
}

func (la *loginAction) login(ctx context.Context) error {
	if la.flags.federatedTokenProvider == azurePipelinesProvider {
		if la.flags.clientID == "" {
			log.Printf("setting client id from environment variable %s", azurePipelinesClientIDEnvVarName)
			la.flags.clientID = os.Getenv(azurePipelinesClientIDEnvVarName)
		}

		if la.flags.tenantID == "" {
			log.Printf("setting tenant id from environment variable %s", azurePipelinesClientIDEnvVarName)
			la.flags.tenantID = os.Getenv(azurePipelinesTenantIDEnvVarName)
		}
	}

	if la.flags.managedIdentity {
		if _, err := la.authManager.LoginWithManagedIdentity(
			ctx, la.flags.clientID,
		); err != nil {
			return fmt.Errorf("logging in: %w", err)
		}

		return nil
	}

	if !la.flags.managedIdentity && la.flags.clientID != "" {
		if la.flags.tenantID == "" {
			return errors.New("must set both `client-id` and `tenant-id` for service principal login")
		}

		if countTrue(
			la.flags.clientSecret.ptr != nil,
			la.flags.clientCertificate != "",
			la.flags.federatedTokenProvider != "",
		) != 1 {
			return fmt.Errorf(
				"must set exactly one of %s for service principal", strings.Join([]string{
					cClientSecretFlagName,
					cClientCertificateFlagName,
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
		case la.flags.federatedTokenProvider == "github":
			if _, err := la.authManager.LoginWithGitHubFederatedTokenProvider(
				ctx, la.flags.tenantID, la.flags.clientID,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}
		case la.flags.federatedTokenProvider == azurePipelinesProvider:
			serviceConnectionID := os.Getenv(azurePipelinesServiceConnectionIDEnvVarName)

			if serviceConnectionID == "" {
				return fmt.Errorf("must set %s for azure-pipelines federated token provider",
					azurePipelinesServiceConnectionIDEnvVarName)
			}

			if _, err := la.authManager.LoginWithAzurePipelinesFederatedTokenProvider(
				ctx, la.flags.tenantID, la.flags.clientID, serviceConnectionID,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}
		}

		return nil
	}

	if la.authManager.UseExternalAuth() {
		// Request a token and assume the external auth system will prompt the user to log in.
		//
		// TODO(ellismg): We may want instead to call some explicit `/login` endpoint on the external auth system instead
		// of abusing the token request in this manner. This would allow the other end to provide a more tailored experience.
		_, err := la.verifyLoggedIn(ctx)
		return err
	}

	useDevCode, err := parseUseDeviceCode(ctx, la.flags.useDeviceCode, la.commandRunner)
	if err != nil {
		return err
	}
	if useDevCode {
		_, err = la.authManager.LoginWithDeviceCode(ctx, la.flags.tenantID, la.flags.scopes, func(url string) error {
			if !la.flags.global.NoPrompt {
				la.console.Message(ctx, "Then press enter and continue to log in from your browser...")
				la.console.WaitForEnter()
				openWithDefaultBrowser(ctx, la.console, url)
				return nil
			}
			// For no-prompt, Just provide instructions without trying to open the browser
			// If manual browsing is enabled, we don't want to open the browser automatically
			la.console.Message(ctx, fmt.Sprintf("Then, go to: %s", url))
			return nil
		})
		return err
	}

	if oneauth.Supported && !la.flags.browser {
		err = la.authManager.LoginWithOneAuth(ctx, la.flags.tenantID, la.flags.scopes)
	} else {
		_, err = la.authManager.LoginInteractive(ctx, la.flags.scopes,
			&auth.LoginInteractiveOptions{
				TenantID:     la.flags.tenantID,
				RedirectPort: la.flags.redirectPort,
				WithOpenUrl: func(url string) error {
					openWithDefaultBrowser(ctx, la.console, url)
					return nil
				},
			})
	}
	if err != nil {
		err = fmt.Errorf("logging in: %w", err)
	}
	return err
}

func parseUseDeviceCode(ctx context.Context, flag boolPtr, commandRunner exec.CommandRunner) (bool, error) {
	var useDevCode bool

	useDevCodeFlag := flag.ptr != nil
	if useDevCodeFlag {
		userInput, err := strconv.ParseBool(*flag.ptr)
		if err != nil {
			return false, fmt.Errorf("unexpected boolean input for '--use-device-code': %w", err)
		}
		// honor the value from the user input. No override.
		return userInput, err
	}

	// Detect cases where the browser isn't available for interactive auth, and we instead want to set `useDeviceCode`
	// to be true by default
	if github.RunningOnCodespaces() {
		// For VSCode online (in web Browser), like GitHub Codespaces or VSCode online attached to any server,
		// interactive browser login will 404 when attempting to redirect to localhost
		// (since azd launches a localhost server running remotely and the login response is accepted locally).
		// Hence, we override login to device-code. See https://github.com/Azure/azure-dev/issues/1006
		useDevCode = runningOnCodespacesBrowser(ctx, commandRunner)
	}

	if runcontext.IsRunningInCloudShell() {
		// Following az CLI behavior in Cloud Shell, use device code authentication when the user is trying to
		// authenticate. The normal interactive authentication flow will not work in Cloud Shell because the browser
		// cannot be opened or (if it could) cannot be redirected back to a port on the Cloud Shell instance.
		return true, nil
	}

	return useDevCode, nil
}
