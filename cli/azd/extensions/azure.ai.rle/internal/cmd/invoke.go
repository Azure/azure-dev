// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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

	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"Creating sandbox for environment %s version %s ...\n",
		state.Name,
		state.EnvironmentVersion,
	); err != nil {
		return err
	}

	sandbox, err := leaseRemoteSandbox(ctx, a.cmd.OutOrStdout(), client, state, a.environmentName == "")
	if err != nil {
		if _, ok := errors.AsType[*azdext.LocalError](err); ok {
			return err
		}
		return serviceError(err)
	}
	defer func() {
		if err := releaseRemoteSandbox(client, state, sandbox.Id); err != nil {
			_, _ = fmt.Fprintf(a.cmd.ErrOrStderr(), "Warning: failed to release sandbox %s: %v\n", sandbox.Id, err)
		}
	}()

	sandboxUrl := strings.TrimRight(sandbox.BaseUrl, "/")
	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"Sandbox %s ready\n",
		sandbox.Id,
	); err != nil {
		return err
	}
	if err := project.WaitForHealth(sandboxUrl, 60*time.Second); err != nil {
		return err
	}
	playgroundUrl, stopPlayground, err := remotePlaygroundUrl(ctx, sandboxUrl)
	if err != nil {
		return err
	}
	defer stopPlayground()
	if err := ui.OpenBrowser(playgroundUrl); err != nil {
		_, _ = fmt.Fprintf(a.cmd.ErrOrStderr(), "Warning: failed to open playground UI: %v\n", err)
	}
	return project.RunShellWithContext(ctx, a.cmd.InOrStdin(), a.cmd.OutOrStdout(), sandboxUrl, a.flags.timeout)
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
	environment, err := resolveLatestEnvironmentByName(a.cmd.Context(), client, environmentName)
	if err != nil {
		return rleState{}, nil, err
	}

	if requestedVersion == "" {
		if err := requireReadyEnvironment(environment, environmentName); err != nil {
			return rleState{}, nil, err
		}
		return rleState{
			Name:               environment.Name,
			ProjectEndpoint:    projectEndpoint,
			EnvironmentId:      environment.Id,
			EnvironmentVersion: environment.Version,
		}, client, nil
	}

	versionedEnvironment, err := client.getEnvironmentVersion(a.cmd.Context(), environmentName, requestedVersion)
	if err != nil {
		return rleState{}, nil, serviceError(err)
	}
	if err := requireReadyEnvironment(versionedEnvironment, environmentName); err != nil {
		return rleState{}, nil, err
	}
	return rleState{
		Name:               versionedEnvironment.Name,
		ProjectEndpoint:    projectEndpoint,
		EnvironmentId:      versionedEnvironment.Id,
		EnvironmentVersion: versionedEnvironment.Version,
	}, client, nil

}

const (
	sandboxStatusRunning            = "Running"
	sandboxStatusFailed             = "Failed"
	diskImageConversionStatusReady  = "Ready"
	diskImageConversionStatusFailed = "Failed"
)

var (
	remoteSandboxCreateTimeout   = 300 * time.Second
	remoteSandboxPollInterval    = 2 * time.Second
	remoteImageConversionTimeout = 15 * time.Minute
	remoteImagePollInterval      = 5 * time.Second
)

func leaseRemoteSandbox(
	ctx context.Context,
	output io.Writer,
	client *rleClient,
	state rleState,
	waitForImage bool,
) (*sandboxResource, error) {
	if waitForImage {
		if err := waitForEnvironmentImage(ctx, output, client, state); err != nil {
			return nil, err
		}
	}

	sandbox, err := client.createSandbox(ctx, state.EnvironmentId, sandboxCreateRequest{
		Version: state.EnvironmentVersion,
	})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sandbox.Id) == "" {
		return nil, &azdext.LocalError{
			Message:    "Control plane did not return a sandbox id.",
			Code:       "rle_sandbox_id_missing",
			Category:   azdext.LocalErrorCategoryInternal,
			Suggestion: "Check the RLE control plane sandbox response, then retry.",
		}
	}
	readySandbox, err := waitForRemoteSandbox(ctx, client, state.EnvironmentId, sandbox)
	if err != nil {
		if releaseErr := releaseRemoteSandbox(client, state, sandbox.Id); releaseErr != nil {
			return nil, fmt.Errorf("%w; additionally failed to release sandbox %s: %w", err, sandbox.Id, releaseErr)
		}
		return nil, err
	}
	return readySandbox, nil
}

func waitForEnvironmentImage(
	ctx context.Context,
	output io.Writer,
	client *rleClient,
	state rleState,
) error {
	if strings.TrimSpace(state.Name) == "" || strings.TrimSpace(state.EnvironmentVersion) == "" {
		return nil
	}

	deadline := time.Now().Add(remoteImageConversionTimeout)
	for {
		environment, err := client.getEnvironmentVersion(ctx, state.Name, state.EnvironmentVersion)
		if err != nil {
			return err
		}

		switch environment.DiskImageConversionStatus {
		case diskImageConversionStatusReady:
			return nil
		case diskImageConversionStatusFailed:
			return &azdext.LocalError{
				Message: fmt.Sprintf(
					"Environment '%s' version '%s' disk image conversion failed: %s",
					state.Name,
					state.EnvironmentVersion,
					firstNonEmpty(environment.DiskImageConversionError, "unknown error"),
				),
				Code:     "rle_disk_image_conversion_failed",
				Category: azdext.LocalErrorCategoryUser,
			}
		}

		if time.Now().After(deadline) {
			return &azdext.LocalError{
				Message: fmt.Sprintf(
					"Environment '%s' version '%s' disk image was not ready after %.0f minutes (last status: %s).",
					state.Name,
					state.EnvironmentVersion,
					remoteImageConversionTimeout.Minutes(),
					firstNonEmpty(environment.DiskImageConversionStatus, "unknown"),
				),
				Code:       "rle_disk_image_conversion_timeout",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Check the environment disk image conversion status, then retry invoke.",
			}
		}

		if _, msgErr := fmt.Fprintf(
			output,
			"Preparing environment disk image (status: %s); waiting %.0f seconds ...\n",
			firstNonEmpty(environment.DiskImageConversionStatus, "unknown"),
			remoteImagePollInterval.Seconds(),
		); msgErr != nil {
			return msgErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(remoteImagePollInterval):
		}
	}
}

func waitForRemoteSandbox(
	ctx context.Context,
	client *rleClient,
	environmentId string,
	sandbox *sandboxResource,
) (*sandboxResource, error) {
	deadline := time.Now().Add(remoteSandboxCreateTimeout)
	for {
		if sandbox.Status == sandboxStatusFailed {
			return nil, &azdext.LocalError{
				Message: fmt.Sprintf(
					"Sandbox %s failed to start: %s",
					sandbox.Id,
					firstNonEmpty(sandbox.Error, "unknown error"),
				),
				Code:     "rle_sandbox_start_failed",
				Category: azdext.LocalErrorCategoryUser,
			}
		}
		if sandbox.Status == sandboxStatusRunning {
			if strings.TrimSpace(sandbox.BaseUrl) == "" {
				return nil, &azdext.LocalError{
					Message:    fmt.Sprintf("Sandbox %s is Running but did not report a data-plane URL.", sandbox.Id),
					Code:       "rle_sandbox_url_missing",
					Category:   azdext.LocalErrorCategoryInternal,
					Suggestion: "Check the RLE control plane sandbox response, then retry.",
				}
			}
			return sandbox, nil
		}
		if time.Now().After(deadline) {
			return nil, &azdext.LocalError{
				Message: fmt.Sprintf(
					"Sandbox %s was not ready after %.0f seconds (last status: %s).",
					sandbox.Id,
					remoteSandboxCreateTimeout.Seconds(),
					firstNonEmpty(sandbox.Status, "unknown"),
				),
				Code:       "rle_sandbox_start_timeout",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Check the RLE control plane sandbox status, then retry.",
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(remoteSandboxPollInterval):
		}

		updated, err := client.getSandbox(ctx, environmentId, sandbox.Id)
		if err != nil {
			return nil, err
		}
		sandbox = updated
	}
}

func releaseRemoteSandbox(client *rleClient, state rleState, sandboxId string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return client.deleteSandbox(ctx, state.EnvironmentId, sandboxId)
}

func remotePlaygroundUrl(ctx context.Context, sandboxUrl string) (string, func(), error) {
	if remoteSandboxHasWeb(ctx, sandboxUrl) {
		return strings.TrimRight(sandboxUrl, "/") + "/web", func() {}, nil
	}
	return startRemotePlaygroundProxy(ctx, sandboxUrl)
}

func remoteSandboxHasWeb(ctx context.Context, sandboxUrl string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(sandboxUrl, "/")+"/web", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}

func startRemotePlaygroundProxy(ctx context.Context, sandboxUrl string) (string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", func() {}, err
	}

	server := &http.Server{
		Handler:           remotePlaygroundHandler(strings.TrimRight(sandboxUrl, "/")),
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
	return "http://" + listener.Addr().String() + "/web", stop, nil
}

func remotePlaygroundHandler(sandboxUrl string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/web" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, ui.RemotePlaygroundHTML)
			return
		}
		proxyOpenEnvToSandbox(w, r, sandboxUrl)
	})
	return mux
}

func proxyOpenEnvToSandbox(w http.ResponseWriter, r *http.Request, sandboxUrl string) {
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

	target, err := http.NewRequestWithContext(r.Context(), r.Method, sandboxUrl+"/"+operation, r.Body) //nolint:gosec // sandboxUrl is the active RLE sandbox URL; operation is restricted above.
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target.Header.Set("Accept", "application/json")
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
	return nil
}
