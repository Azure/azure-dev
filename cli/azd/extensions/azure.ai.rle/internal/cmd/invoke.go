// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"time"

	"azure.ai.rle/internal/project"
	"azure.ai.rle/internal/ui"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type remoteInvokeFlags struct {
	timeout int
	version string
}

type remoteInvokeAction struct {
	cmd             *cobra.Command
	flags           *remoteInvokeFlags
	environmentName string
}

var validateSandboxURL = validateRemoteSandboxURL

func newInvokeCommand() *cobra.Command {
	flags := &remoteInvokeFlags{
		timeout: 30,
	}

	cmd := &cobra.Command{
		Use:   "invoke [environment-name]",
		Short: "Open a remote OpenEnv runtime shell",
		Long: `Open a remote OpenEnv runtime shell.

With no environment name, invoke uses the environment saved in .azd-rle.json.
To invoke an existing environment without local source or state, provide its name
and set FOUNDRY_PROJECT_ENDPOINT. Use --version to select a specific published
version; otherwise, the latest version returned by the project is used.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			environmentName := ""
			if len(args) == 1 {
				environmentName = args[0]
			}
			return (&remoteInvokeAction{
				cmd:             cmd,
				flags:           flags,
				environmentName: environmentName,
			}).Run()
		},
	}

	cmd.Flags().IntVar(
		&flags.timeout,
		"timeout",
		flags.timeout,
		"Per-command OpenEnv request timeout in seconds (0 for no timeout).",
	)
	cmd.Flags().StringVar(
		&flags.version,
		"version",
		"",
		"Published environment version to invoke.",
	)
	return cmd
}

func (a *remoteInvokeAction) Run() error {
	state, client, err := a.resolveTarget()
	if err != nil {
		return err
	}

	ctx, stopSignals := signal.NotifyContext(a.cmd.Context(), os.Interrupt)
	defer stopSignals()

	runtimeTarget := fmt.Sprintf("environment %s using the latest version", state.EnvironmentName)
	if state.runtimeRouteVersion != "" {
		runtimeTarget = fmt.Sprintf("environment %s version %s", state.EnvironmentName, state.runtimeRouteVersion)
	}
	if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "Creating runtime for %s ...\n", runtimeTarget); err != nil {
		return err
	}

	runtime, err := createRemoteRuntime(ctx, client, state, a.cmd.OutOrStdout())
	if runtime != nil {
		defer func() {
			writeCleanupResult(a.cmd.ErrOrStderr(), cleanupRemoteRuntime(client, state.EnvironmentName, runtime))
		}()
	}
	if err != nil {
		if _, ok := errors.AsType[*azdext.LocalError](err); ok {
			return err
		}
		if isRleNotFound(err) {
			if state.runtimeRouteVersion != "" {
				return environmentVersionNotFoundError(state.EnvironmentName, state.runtimeRouteVersion)
			}
			return environmentNotFoundError(state.EnvironmentName)
		}
		return serviceError(err)
	}

	instanceUrl := strings.TrimRight(runtime.instance.BaseUrl, "/")
	if err := validateSandboxURL(instanceUrl, state.ProjectEndpoint); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(
		a.cmd.OutOrStdout(),
		"Environment instance is running; waiting for OpenEnv runtime ...",
	); err != nil {
		return err
	}
	instanceUrl, err = withFoundryAPIVersion(instanceUrl)
	if err != nil {
		return err
	}
	if err := project.WaitForHealthWithAuthorizationProvider(
		ctx,
		instanceUrl,
		remoteRuntimeHealthTimeout,
		client.authorizationHeader,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"Environment %s version %s ready\n",
		state.EnvironmentName,
		runtime.group.EnvironmentVersion,
	); err != nil {
		return err
	}
	playgroundUrl, stopPlayground, err := remotePlaygroundUrlWithAuthorizationProvider(
		ctx,
		instanceUrl,
		client.authorizationHeader,
	)
	if err != nil {
		return err
	}
	defer stopPlayground()
	if err := ui.OpenBrowser(playgroundUrl); err != nil {
		_, _ = fmt.Fprintf(a.cmd.ErrOrStderr(), "Warning: failed to open playground UI: %v\n", err)
	}
	return project.RunShellWithContextAndAuthorizationProvider(
		ctx,
		a.cmd.InOrStdin(),
		a.cmd.OutOrStdout(),
		instanceUrl,
		a.flags.timeout,
		client.authorizationHeader,
	)
}

func (a *remoteInvokeAction) resolveTarget() (rleState, *rleClient, error) {
	requestedVersion := strings.TrimSpace(a.flags.version)
	if a.cmd.Flags().Changed("version") && requestedVersion == "" {
		return rleState{}, nil, &azdext.LocalError{
			Message:    "--version requires a non-empty environment version.",
			Code:       "rle_environment_version_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Provide a semantic version, for example --version 2.1.0.",
		}
	}
	if strings.TrimSpace(a.environmentName) == "" && requestedVersion != "" {
		return rleState{}, nil, &azdext.LocalError{
			Message:    "--version requires an environment name.",
			Code:       "rle_environment_name_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run azd ai rle invoke <environment-name> --version <version>.",
		}
	}

	if strings.TrimSpace(a.environmentName) == "" {
		state, err := loadRleState()
		if err != nil {
			return rleState{}, nil, err
		}
		if err := requireDeployedEnvironment(state); err != nil {
			return rleState{}, nil, err
		}
		state.runtimeRouteVersion = state.EnvironmentVersion
		client, err := createRleClient(state.ProjectEndpoint)
		return state, client, err
	}

	environmentName := strings.TrimSpace(a.environmentName)
	projectEndpoint, err := resolveEnvironmentListProjectEndpoint()
	if err != nil {
		return rleState{}, nil, err
	}
	client, err := createRleClient(projectEndpoint)
	if err != nil {
		return rleState{}, nil, err
	}

	if requestedVersion == "" {
		return rleState{
			EnvironmentName: environmentName,
			ProjectEndpoint: projectEndpoint,
		}, client, nil
	}

	versionedEnvironment, err := client.getEnvironmentVersion(a.cmd.Context(), environmentName, requestedVersion)
	if isRleNotFound(err) {
		return rleState{}, nil, environmentVersionNotFoundError(environmentName, requestedVersion)
	}
	if err != nil {
		return rleState{}, nil, serviceError(err)
	}
	if responseName := strings.TrimSpace(versionedEnvironment.Name); responseName != "" && responseName != environmentName {
		return rleState{}, nil, unexpectedEnvironmentVersionIdentity(
			environmentName,
			requestedVersion,
			responseName,
			versionedEnvironment.Version,
		)
	}
	if responseVersion := strings.TrimSpace(versionedEnvironment.Version); responseVersion != "" &&
		responseVersion != requestedVersion {
		return rleState{}, nil, unexpectedEnvironmentVersionIdentity(
			environmentName,
			requestedVersion,
			versionedEnvironment.Name,
			responseVersion,
		)
	}
	return rleState{
		EnvironmentName:     environmentName,
		ProjectEndpoint:     projectEndpoint,
		EnvironmentId:       versionedEnvironment.Id,
		EnvironmentVersion:  requestedVersion,
		runtimeRouteVersion: requestedVersion,
	}, client, nil

}

func unexpectedEnvironmentVersionIdentity(
	requestedName string,
	requestedVersion string,
	responseName string,
	responseVersion string,
) error {
	return &azdext.LocalError{
		Message: fmt.Sprintf(
			"RLE service returned environment %q version %q for requested environment %q version %q.",
			responseName,
			responseVersion,
			requestedName,
			requestedVersion,
		),
		Code:       "rle_environment_version_mismatch",
		Category:   azdext.LocalErrorCategoryInternal,
		Suggestion: "Retry the command. If the problem persists, report the mismatched RLE response.",
	}
}

const (
	instanceStatusRunning     = "Running"
	instanceStatusFailed      = "Failed"
	instanceStatusDeleted     = "Deleted"
	remoteReadinessRetryCount = 10
	playgroundSessionCookie   = "azd-rle-playground-session"
)

var (
	remoteInstanceCreateTimeout  = 300 * time.Second
	remoteInstancePollInterval   = 2 * time.Second
	remoteReadinessRetryInterval = 10 * time.Second
	remoteRuntimeHealthTimeout   = 60 * time.Second
)

type remoteRuntime struct {
	group        *instanceGroupResource
	instance     *instanceResource
	routeVersion string
}

func createRemoteRuntime(
	ctx context.Context,
	client *rleClient,
	state rleState,
	output io.Writer,
) (*remoteRuntime, error) {
	group, err := createRemoteInstanceGroup(ctx, client, state, output)
	if err != nil {
		return nil, err
	}
	runtime := &remoteRuntime{
		group:        group,
		routeVersion: state.runtimeRouteVersion,
	}
	if err := validateInstanceGroupIdentity(state, group); err != nil {
		return runtime, err
	}

	instance, err := client.createInstance(ctx, state.EnvironmentName, group.EnvironmentVersion, group.Id)
	if err != nil {
		return runtime, err
	}
	runtime.instance = instance
	if strings.TrimSpace(instance.InstanceId) == "" {
		return runtime, &azdext.LocalError{
			Message:    "RLE service did not return an instance id.",
			Code:       "rle_instance_id_missing",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Check the RLE service instance response, then retry.",
		}
	}
	readyInstance, err := waitForRemoteInstance(ctx, client, state.EnvironmentName, group, instance)
	if readyInstance != nil {
		runtime.instance = readyInstance
	}
	return runtime, err
}

func validateInstanceGroupIdentity(state rleState, group *instanceGroupResource) error {
	if strings.TrimSpace(group.Id) == "" {
		return &azdext.LocalError{
			Message:    "RLE service did not return an instance group id.",
			Code:       "rle_instance_group_id_missing",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Check the RLE service instance group response, then retry.",
		}
	}
	if responseName := strings.TrimSpace(group.EnvironmentName); responseName != "" &&
		responseName != state.EnvironmentName {
		return &azdext.LocalError{
			Message: fmt.Sprintf(
				"RLE service returned environment %q for requested environment %q.",
				responseName,
				state.EnvironmentName,
			),
			Code:       "rle_instance_group_environment_mismatch",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Check the RLE service instance group response, then retry.",
		}
	}
	if strings.TrimSpace(group.EnvironmentVersion) == "" {
		return &azdext.LocalError{
			Message:    "RLE service did not return the resolved environment version.",
			Code:       "rle_instance_group_version_missing",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Check the RLE service instance group response, then retry.",
		}
	}
	if requestedVersion := strings.TrimSpace(state.runtimeRouteVersion); requestedVersion != "" &&
		group.EnvironmentVersion != requestedVersion {
		return &azdext.LocalError{
			Message: fmt.Sprintf(
				"RLE service returned environment version %q for requested version %q.",
				group.EnvironmentVersion,
				requestedVersion,
			),
			Code:       "rle_instance_group_version_mismatch",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Check the RLE service instance group response, then retry.",
		}
	}
	return nil
}

func createRemoteInstanceGroup(
	ctx context.Context,
	client *rleClient,
	state rleState,
	output io.Writer,
) (*instanceGroupResource, error) {
	for attempt := 0; ; attempt++ {
		group, err := client.createInstanceGroup(ctx, state.EnvironmentName, state.runtimeRouteVersion)
		if !isEnvironmentNotReadyError(err) {
			return group, err
		}
		if attempt >= remoteReadinessRetryCount {
			return nil, &azdext.LocalError{
				Message: fmt.Sprintf(
					"Environment %q was not ready after %d retries while creating the runtime.",
					state.EnvironmentName,
					remoteReadinessRetryCount,
				),
				Code:     "rle_environment_readiness_timeout",
				Category: azdext.LocalErrorCategoryUser,
				Suggestion: fmt.Sprintf(
					"Run azd ai rle show %s to inspect the disk image status, then retry.",
					state.EnvironmentName,
				),
			}
		}
		if output != nil {
			_, _ = fmt.Fprintf(
				output,
				"The requested environment's disk image is not ready yet. Waiting %.0f seconds before retrying (%d/%d) ...\n",
				remoteReadinessRetryInterval.Seconds(),
				attempt+1,
				remoteReadinessRetryCount,
			)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(remoteReadinessRetryInterval):
		}
	}
}

func isEnvironmentNotReadyError(err error) bool {
	httpErr, ok := errors.AsType[*rleHTTPError](err)
	return ok && httpErr.statusCode == http.StatusBadRequest && httpErr.code() == "EnvironmentNotReady"
}

func isRleNotFound(err error) bool {
	httpErr, ok := errors.AsType[*rleHTTPError](err)
	return ok && httpErr.statusCode == http.StatusNotFound
}

func waitForRemoteInstance(
	ctx context.Context,
	client *rleClient,
	environmentName string,
	group *instanceGroupResource,
	instance *instanceResource,
) (*instanceResource, error) {
	deadline := time.Now().Add(remoteInstanceCreateTimeout)
	for {
		if instance.Status == instanceStatusFailed {
			return nil, &azdext.LocalError{
				Message: fmt.Sprintf(
					"Environment instance failed to start: %s",
					firstNonEmpty(instance.Error, "unknown error"),
				),
				Code:     "rle_instance_start_failed",
				Category: azdext.LocalErrorCategoryUser,
			}
		}
		if instance.Status == instanceStatusDeleted {
			return nil, &azdext.LocalError{
				Message:  "Environment instance was deleted before it became ready.",
				Code:     "rle_instance_start_deleted",
				Category: azdext.LocalErrorCategoryUser,
			}
		}
		if instance.Status == instanceStatusRunning {
			if strings.TrimSpace(instance.BaseUrl) == "" {
				return nil, &azdext.LocalError{
					Message:    "Environment instance is Running but did not report a data-plane URL.",
					Code:       "rle_instance_url_missing",
					Category:   azdext.LocalErrorCategoryInternal,
					Suggestion: "Check the RLE service instance response, then retry.",
				}
			}
			return instance, nil
		}
		if time.Now().After(deadline) {
			return nil, &azdext.LocalError{
				Message: fmt.Sprintf(
					"Environment instance was not ready after %.0f seconds (last status: %s).",
					remoteInstanceCreateTimeout.Seconds(),
					firstNonEmpty(instance.Status, "unknown"),
				),
				Code:       "rle_instance_start_timeout",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Check the RLE service instance status, then retry.",
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(remoteInstancePollInterval):
		}

		updated, err := client.getInstance(
			ctx,
			environmentName,
			group.EnvironmentVersion,
			group.Id,
			instance.InstanceId,
		)
		if err != nil {
			return nil, err
		}
		instance = updated
	}
}

func cleanupRemoteRuntime(client *rleClient, environmentName string, runtime *remoteRuntime) error {
	if runtime == nil || runtime.group == nil || strings.TrimSpace(runtime.group.Id) == "" {
		return errors.New("cleanup requires an instance group id")
	}
	environmentVersion := firstNonEmpty(runtime.routeVersion, runtime.group.EnvironmentVersion)

	var instanceErr error
	if runtime.instance != nil && strings.TrimSpace(runtime.instance.InstanceId) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		instanceErr = client.deleteInstance(
			ctx,
			environmentName,
			environmentVersion,
			runtime.group.Id,
			runtime.instance.InstanceId,
		)
		cancel()
		if isRleNotFound(instanceErr) {
			instanceErr = nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	groupErr := client.deleteInstanceGroup(
		ctx,
		environmentName,
		environmentVersion,
		runtime.group.Id,
	)
	if isRleNotFound(groupErr) {
		groupErr = nil
	}
	return errors.Join(instanceErr, groupErr)
}

func writeCleanupResult(writer io.Writer, err error) {
	if err != nil {
		_, _ = fmt.Fprintln(writer, "Warning: remote runtime cleanup could not be completed; resources may remain.")
		return
	}
	_, _ = fmt.Fprintln(
		writer,
		"Temporary remote runtime resources cleaned up successfully. Local environment files and state were unchanged.",
	)
}

func remotePlaygroundUrlWithAuthorizationProvider(
	ctx context.Context,
	sandboxUrl string,
	authorizationProvider project.AuthorizationProvider,
) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", func() {}, err
	}
	sessionToken, err := newPlaygroundSessionToken()
	if err != nil {
		_ = listener.Close()
		return "", func() {}, err
	}

	server := &http.Server{
		Handler: remotePlaygroundHandler(
			strings.TrimRight(sandboxUrl, "/"),
			authorizationProvider,
			listener.Addr().String(),
			sessionToken,
		),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The shell remains usable even if the optional local UI proxy exits.
		}
	}()

	stop := func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}
	return "http://" + listener.Addr().String() + "/web?token=" + url.QueryEscape(sessionToken), stop, nil
}

func remotePlaygroundHandler(
	sandboxUrl string,
	authorizationProvider project.AuthorizationProvider,
	expectedHost string,
	sessionToken string,
) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !validateLoopbackPlaygroundRequest(w, r, expectedHost) {
			return
		}
		if !authorizeLoopbackPlaygroundRequest(w, r, sessionToken) {
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/web" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, ui.RemotePlaygroundHTML)
			return
		}
		proxyOpenEnvToSandbox(w, r, sandboxUrl, authorizationProvider)
	})
	return mux
}

func validateLoopbackPlaygroundRequest(w http.ResponseWriter, r *http.Request, expectedHost string) bool {
	if !strings.EqualFold(r.Host, expectedHost) {
		http.Error(w, "invalid host", http.StatusForbidden)
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	originUrl, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(originUrl.Scheme, "http") || !strings.EqualFold(originUrl.Host, expectedHost) {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return false
	}
	return true
}

func authorizeLoopbackPlaygroundRequest(w http.ResponseWriter, r *http.Request, sessionToken string) bool {
	if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/web") {
		if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
			if token != sessionToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return false
			}
			http.SetCookie(w, &http.Cookie{
				Name:     playgroundSessionCookie,
				Value:    sessionToken,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
			target := *r.URL
			values := target.Query()
			values.Del("token")
			target.RawQuery = values.Encode()
			if target.Path == "" {
				target.Path = "/web"
			}
			http.Redirect(w, r, target.String(), http.StatusSeeOther)
			return false
		}
	}
	cookie, err := r.Cookie(playgroundSessionCookie)
	if err != nil || cookie.Value != sessionToken {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func newPlaygroundSessionToken() (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("create playground session token: %w", err)
	}
	return hex.EncodeToString(token), nil
}

func proxyOpenEnvToSandbox(
	w http.ResponseWriter,
	r *http.Request,
	sandboxUrl string,
	authorizationProvider project.AuthorizationProvider,
) {
	operation := strings.Trim(r.URL.Path, "/")
	switch operation {
	case "health", "state", "metadata", "schema":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	case "reset", "step":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}

	targetUrl, err := project.RuntimeOperationURL(sandboxUrl, operation)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target, err := http.NewRequestWithContext(r.Context(), r.Method, targetUrl, r.Body) //nolint:gosec // sandboxUrl is the active RLE sandbox URL; operation is restricted above.
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target.Header.Set("Accept", "application/json")
	if authorizationProvider != nil {
		authorization, err := authorizationProvider(r.Context())
		if err != nil {
			http.Error(w, "failed to authenticate to environment runtime", http.StatusBadGateway)
			return
		}
		target.Header.Set("Authorization", authorization)
	}
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		target.Header.Set("Content-Type", contentType)
	}
	resp, err := project.HTTPClient(60).Do(target) //nolint:gosec // local UI proxy intentionally forwards only fixed OpenEnv operations to the active sandbox.
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func withFoundryAPIVersion(runtimeUrl string) (string, error) {
	parsedUrl, err := url.Parse(runtimeUrl)
	if err != nil {
		return "", fmt.Errorf("parse environment runtime URL: %w", err)
	}
	query := parsedUrl.Query()
	query.Set("api-version", foundryAPIVersion)
	parsedUrl.RawQuery = query.Encode()
	return parsedUrl.String(), nil
}

func validateRemoteSandboxURL(sandboxUrl string, projectEndpoint string) error {
	sandbox, err := url.Parse(sandboxUrl)
	if err != nil {
		return fmt.Errorf("parse sandbox URL: %w", err)
	}
	projectUrl, err := url.Parse(projectEndpoint)
	if err != nil {
		return fmt.Errorf("parse Foundry project endpoint: %w", err)
	}

	isTrustedProjectOrigin := strings.EqualFold(sandbox.Scheme, "https") &&
		sandbox.Port() == "" &&
		strings.EqualFold(sandbox.Scheme, projectUrl.Scheme) &&
		strings.EqualFold(sandbox.Host, projectUrl.Host)
	if sandbox.User != nil || !isTrustedProjectOrigin {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("RLE returned an untrusted sandbox URL: %s", sandboxUrl),
			Code:       "rle_sandbox_url_untrusted",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Check the RLE service runtime response, then retry.",
		}
	}
	return nil
}

func requireDeployedEnvironment(state rleState) error {
	if strings.TrimSpace(state.ProjectEndpoint) == "" {
		return &azdext.LocalError{
			Message:    "Foundry project endpoint is required for remote invoke.",
			Code:       "rle_project_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run azd ai rle publish first with FOUNDRY_PROJECT_ENDPOINT set.",
		}
	}
	if strings.TrimSpace(state.EnvironmentId) == "" {
		return &azdext.LocalError{
			Message:    "RLE environment has not been deployed.",
			Code:       "rle_environment_not_deployed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run azd ai rle publish from this environment folder first.",
		}
	}
	if strings.TrimSpace(state.EnvironmentName) == "" {
		return &azdext.LocalError{
			Message:    "The deployed RLE environment does not include a name.",
			Code:       "rle_environment_name_missing",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run azd ai rle publish again to refresh the local deployment state.",
		}
	}
	if strings.TrimSpace(state.EnvironmentVersion) == "" {
		return &azdext.LocalError{
			Message:    "The deployed RLE environment does not include a version.",
			Code:       "rle_environment_version_missing",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run azd ai rle publish again to refresh the local deployment state.",
		}
	}
	return nil
}
