// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
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
		la.flags.scopes = auth.LoginScopes
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
		// Ensure ID token is valid, and can be exchanged for an access token
		token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
			Scopes: la.flags.scopes,
		})

		if err != nil {
			log.Printf("failed to check auth status: %s", err.Error())
			res.Status = contracts.LoginStatusUnauthenticated
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

// runningOnCodespacesBrowser use `code --status` which returns:
//
//	> The --status argument is not yet supported in browsers.
//
// to detect when vscode is within a WebBrowser environment.
func runningOnCodespacesBrowser(ctx context.Context, commandRunner exec.CommandRunner) bool {

	if os.Getenv("AZD_SEMI_MANUAL_LOGIN") == "true" {
		return true
	}

	if os.Getenv("CODESPACES") != "true" {
		return false
	}

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
	if la.flags.clientID != "" {
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
		case la.flags.federatedTokenProvider != "":
			if _, err := la.authManager.LoginWithServicePrincipalFederatedTokenProvider(
				ctx, la.flags.tenantID, la.flags.clientID, la.flags.federatedTokenProvider,
			); err != nil {
				return fmt.Errorf("logging in: %w", err)
			}
		}

		return nil
	}

	useDevCode, err := parseUseDeviceCode(ctx, la.flags.useDeviceCode)
	if err != nil {
		return err
	}

	if useDevCode {
		_, err := la.authManager.LoginWithDeviceCode(ctx, la.writer, la.flags.tenantID, la.flags.scopes)
		if err != nil {
			return fmt.Errorf("logging in: %w", err)
		}
	} else {
		codespacesInBrowser := runningOnCodespacesBrowser(ctx, la.commandRunner)

		var loginError error
		if !codespacesInBrowser {
			_, loginError = la.authManager.LoginInteractive(ctx, la.flags.redirectPort, la.flags.tenantID, la.flags.scopes)
		} else {
			loginError = semiManualLogin(
				ctx, la.console, la.flags.redirectPort, func(port int) (azcore.TokenCredential, error) {
					return la.authManager.LoginInteractive(ctx, port, la.flags.tenantID, la.flags.scopes)
				})
		}
		if loginError != nil {
			return loginError
		}
	}

	return nil
}

func parseUseDeviceCode(ctx context.Context, flag boolPtr) (bool, error) {
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

	return useDevCode, nil
}

func findFreePort() (int, error) {
	var l net.Listener
	var err error
	var port int
	for i := 0; i < 10; i++ {
		l, err = net.Listen("tcp", "localhost:0")
		if err != nil {
			continue
		}
		// release port so it can be used by login
		defer l.Close()
		addr := l.Addr().String()
		port, err = strconv.Atoi(addr[strings.LastIndex(addr, ":")+1:])
		if err != nil {
			return 0, err
		}
		break
	}
	return port, nil
}

func semiManualLogin(
	ctx context.Context,
	console input.Console,
	userSelectedPort int,
	loginFunc func(redirectPort int) (azcore.TokenCredential, error)) error {

	loginPort := userSelectedPort
	if loginPort == 0 {
		overridePort, err := findFreePort()
		if err != nil {
			return err
		}
		loginPort = overridePort
	}
	interactiveLogin := async.NewTask(func(taskContext *async.TaskContext[bool]) {
		_, err := loginFunc(loginPort)
		if err != nil {
			taskContext.SetError(err)
			return
		}
		taskContext.SetResult(true)
	})
	if err := interactiveLogin.Run(); err != nil {
		return err
	}

	console.Message(ctx, "running semi-manual login flow. Please follow next steps:")
	console.Message(ctx, "  1) Login from web browser. After login, you will see a 404 error in the browser.")
	console.Message(ctx, "  2) Copy the url from the browser when you see the 404 error.")
	console.Message(ctx, "  3) Paste url below (in this terminal).")

	loginUrl := ""
	for loginUrl == "" {
		inputLoginUrl, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:    "Url from browser:",
			IsPassword: true,
		})
		if err != nil {
			return err
		}
		url, err := url.Parse(inputLoginUrl)
		if err != nil {
			console.Message(ctx, "unexpected url format found. Try again.")
			continue
		}
		expectedHostName := "localhost"
		if url.Hostname() != expectedHostName {
			console.Message(ctx, fmt.Sprintf("Expecting hostname: '%s' in the url. Try again.", expectedHostName))
			continue
		}
		expectedPort := strconv.Itoa(loginPort)
		if url.Port() != expectedPort {
			console.Message(ctx, fmt.Sprintf("Expecting port number: '%s' in the url. Try again.", expectedPort))
			continue
		}
		expectedQuery := "code"
		if !url.Query().Has(expectedQuery) {
			console.Message(ctx, fmt.Sprintf("Expecting query parameter '%s' in the url. Try again.", expectedQuery))
			continue
		}
		loginUrl = inputLoginUrl
	}

	_, err := http.DefaultClient.Get(loginUrl)
	if err != nil {
		return fmt.Errorf("doing semi-manual login: doing HTTP GET: %w", err)
	}

	_, err = interactiveLogin.Await()
	return err
}
