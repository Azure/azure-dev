// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore openenv

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type openEnvInvokeFlags struct {
	port         int
	timeout      int
	action       string
	body         string
	name         string
	source       string
	dockerfile   string
	watch        bool
	restart      bool
	reuseRunning bool
}

func newInvokeCommand() *cobra.Command {
	flags := &openEnvInvokeFlags{
		timeout:      30,
		reuseRunning: true,
	}

	cmd := &cobra.Command{
		Use:   "invoke",
		Short: "Open a remote OpenEnv runtime shell",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return invokeRemoteEnvironment(cmd, flags)
		},
	}

	cmd.Flags().IntVar(
		&flags.timeout,
		"timeout",
		flags.timeout,
		"Per-command OpenEnv request timeout in seconds (0 for no timeout).",
	)
	return cmd
}

func invokeRemoteEnvironment(cmd *cobra.Command, flags *openEnvInvokeFlags) error {
	state, err := loadRleState()
	if err != nil {
		return err
	}
	if err := requireDeployedEnvironment(state); err != nil {
		return err
	}

	ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stopSignals()

	client := newRleClient(resolveControlPlaneEndpoint())
	if _, err := fmt.Fprintf(
		cmd.OutOrStdout(),
		"Creating sandbox for environment %s ...\n",
		state.EnvironmentId,
	); err != nil {
		return err
	}

	sandbox, err := leaseRemoteSandbox(ctx, cmd.OutOrStdout(), client, state)
	if err != nil {
		if _, ok := errors.AsType[*azdext.LocalError](err); ok {
			return err
		}
		return serviceError(err)
	}
	defer func() {
		if err := releaseRemoteSandbox(client, state, sandbox.Id); err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to release sandbox %s: %v\n", sandbox.Id, err)
		}
	}()

	sandboxUrl := strings.TrimRight(firstNonEmpty(sandbox.Url, sandbox.Endpoint), "/")
	if _, err := fmt.Fprintf(
		cmd.OutOrStdout(),
		"Sandbox %s ready at %s\n",
		sandbox.Id,
		sandboxUrl,
	); err != nil {
		return err
	}
	if err := waitForOpenEnvHealth(sandboxUrl, 60*time.Second); err != nil {
		return err
	}
	playgroundUrl, stopPlayground, err := remotePlaygroundUrl(ctx, sandboxUrl)
	if err != nil {
		return err
	}
	defer stopPlayground()
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Playground UI: %s\n", playgroundUrl); err != nil {
		return err
	}
	if err := openBrowserFunc(playgroundUrl); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to open playground UI: %v\n", err)
	}
	return runOpenEnvShellWithContext(ctx, cmd.InOrStdin(), cmd.OutOrStdout(), sandboxUrl, flags.timeout)
}

const (
	sandboxStatusRunning = "Running"
	sandboxStatusFailed  = "Failed"
)

var (
	remoteSandboxCreateTimeout = 300 * time.Second
	remoteSandboxPollInterval  = 2 * time.Second
	remoteImagePollInterval    = 5 * time.Second
)

func leaseRemoteSandbox(
	ctx context.Context,
	output io.Writer,
	client *rleClient,
	state rleState,
) (*sandboxResource, error) {
	sandbox, err := createSandboxWhenImageReady(ctx, output, client, state)
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
	readySandbox, err := waitForRemoteSandbox(ctx, client, state.Project, state.EnvironmentId, sandbox)
	if err != nil {
		if releaseErr := releaseRemoteSandbox(client, state, sandbox.Id); releaseErr != nil {
			return nil, fmt.Errorf("%w; additionally failed to release sandbox %s: %v", err, sandbox.Id, releaseErr)
		}
		return nil, err
	}
	return readySandbox, nil
}

func createSandboxWhenImageReady(
	ctx context.Context,
	output io.Writer,
	client *rleClient,
	state rleState,
) (*sandboxResource, error) {
	deadline := time.Now().Add(remoteSandboxCreateTimeout)
	attempt := 0
	for {
		sandbox, err := client.createSandbox(ctx, state.Project, state.EnvironmentId, sandboxCreateRequest{
			Version: state.EnvironmentVersion,
		})
		if err == nil {
			return sandbox, nil
		}

		status, pending := sandboxLeasePendingStatus(err)
		if !pending {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, &azdext.LocalError{
				Message: fmt.Sprintf(
					"Environment image conversion was not ready after %.0f seconds (last status: %s).",
					remoteSandboxCreateTimeout.Seconds(),
					firstNonEmpty(status, "unknown"),
				),
				Code:       "rle_disk_image_conversion_timeout",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Wait for the RLE control plane image conversion to finish, then retry invoke.",
			}
		}

		attempt++
		if _, msgErr := fmt.Fprintf(
			output,
			"Environment image conversion is %s; waiting %.0f seconds before retrying sandbox lease (attempt %d) ...\n",
			firstNonEmpty(status, "not ready"),
			remoteImagePollInterval.Seconds(),
			attempt,
		); msgErr != nil {
			return nil, msgErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(remoteImagePollInterval):
		}
	}
}

func sandboxLeasePendingStatus(err error) (string, bool) {
	httpErr, ok := errors.AsType[*rleHTTPError](err)
	if !ok || httpErr.statusCode != http.StatusConflict {
		return "", false
	}
	status := strings.TrimSpace(httpErr.body)
	if before, after, found := strings.Cut(httpErr.body, "conversion status:"); found {
		_ = before
		status = strings.TrimSpace(strings.Trim(strings.Split(after, ")")[0], `."}`))
	}
	return status, true
}

func waitForRemoteSandbox(
	ctx context.Context,
	client *rleClient,
	project string,
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
			if strings.TrimSpace(firstNonEmpty(sandbox.Url, sandbox.Endpoint)) == "" {
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

		updated, err := client.getSandbox(ctx, project, environmentId, sandbox.Id)
		if err != nil {
			return nil, err
		}
		sandbox = updated
	}
}

func releaseRemoteSandbox(client *rleClient, state rleState, sandboxId string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return client.deleteSandbox(ctx, state.Project, state.EnvironmentId, sandboxId)
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
		Handler: remotePlaygroundHandler(strings.TrimRight(sandboxUrl, "/")),
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// The shell remains usable even if the optional local UI proxy exits.
		}
	}()

	stop := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
			_, _ = io.WriteString(w, remotePlaygroundHTML)
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

	target, err := http.NewRequestWithContext(r.Context(), r.Method, sandboxUrl+"/"+operation, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target.Header.Set("Accept", "application/json")
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		target.Header.Set("Content-Type", contentType)
	}
	resp, err := openEnvHTTPClient(60).Do(target)
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

const remotePlaygroundHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>RLE Remote Console</title>
  <style>
    body { margin: 0; background: #070b10; color: #d7dee8; font: 13px/1.45 system-ui, sans-serif; }
    header { display: flex; justify-content: space-between; padding: 14px 18px; background: #0b1118; border-bottom: 1px solid #1c2633; font-weight: 700; }
    main { display: grid; grid-template-columns: minmax(360px, 1fr) minmax(440px, 1fr); min-height: calc(100vh - 49px); }
    section { padding: 22px; border-right: 1px solid #1c2633; }
    h2, label { color: #7f8da3; text-transform: uppercase; font-size: 12px; letter-spacing: .06em; }
    textarea, pre, input { width: 100%; border: 1px solid #1c2633; border-radius: 8px; background: #060a0f; color: #d7dee8; font: 12px/1.45 Consolas, monospace; }
    textarea { min-height: 150px; padding: 12px; }
    pre { min-height: 260px; padding: 14px; white-space: pre-wrap; overflow: auto; }
    input { width: 120px; padding: 8px; }
    button { margin: 8px 8px 8px 0; border: 1px solid #1c2633; border-radius: 8px; background: #0f1620; color: #d7dee8; padding: 8px 16px; font-weight: 700; cursor: pointer; }
    button.primary { background: #2f81f7; color: white; }
  </style>
</head>
<body>
  <header><span>RLE Remote Console</span><span>local UI proxy</span></header>
  <main>
    <section>
      <h2>Action</h2>
      <label>seed</label>
      <input id="seedInput" type="number" placeholder="optional" />
      <button onclick="resetEnv()">Reset</button>
      <button onclick="call('GET','/state')">State</button>
      <button onclick="call('GET','/schema', undefined, 'schema')">Schema</button>
      <label>Action JSON</label>
      <textarea id="actionBody">{}</textarea>
      <p><button class="primary" onclick="stepEnv()">Step</button></p>
      <h2>Schema</h2>
      <pre id="schema">Not loaded.</pre>
    </section>
    <section>
      <h2>Output</h2>
      <pre id="output">Ready.</pre>
    </section>
  </main>
  <script>
    call("GET", "/state");
    call("GET", "/schema", undefined, "schema").then((schema) => {
      if (schema && schema.properties && document.getElementById("actionBody").value.trim() === "{}") {
        const sample = {};
        for (const [name, prop] of Object.entries(schema.properties)) {
          if (prop.default !== undefined) sample[name] = prop.default;
        }
        document.getElementById("actionBody").value = JSON.stringify(sample, null, 2);
      }
    });
    function resetEnv() {
      const seed = document.getElementById("seedInput").value;
      call("POST", "/reset", seed === "" ? {} : { seed: Number(seed) });
    }
    function stepEnv() {
      try { call("POST", "/step", { action: JSON.parse(document.getElementById("actionBody").value || "{}") }); }
      catch (err) { document.getElementById("output").textContent = "Action is not valid JSON: " + err.message; }
    }
    async function call(method, path, body, targetId) {
      const target = document.getElementById(targetId || "output");
      target.textContent = "Calling " + method + " " + path + " ...";
      const options = { method, headers: { "Accept": "application/json" } };
      if (body !== undefined) { options.headers["Content-Type"] = "application/json"; options.body = JSON.stringify(body); }
      const response = await fetch(path, options);
      const text = await response.text();
      try { const json = JSON.parse(text); target.textContent = JSON.stringify(json, null, 2); return json; }
      catch { target.textContent = response.status + " " + response.statusText + "\n" + text; }
    }
  </script>
</body>
</html>`

func requireDeployedEnvironment(state rleState) error {
	if strings.TrimSpace(state.Project) == "" {
		return &azdext.LocalError{
			Message:    "RLE project is required for remote invoke.",
			Code:       "rle_project_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run azd ai rle deploy --project-id <project-id> first.",
		}
	}
	if strings.TrimSpace(state.EnvironmentId) == "" {
		return &azdext.LocalError{
			Message:    "RLE environment has not been deployed.",
			Code:       "rle_environment_not_deployed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run azd ai rle deploy from this environment folder first.",
		}
	}
	return nil
}

func runOpenEnvShellWithContext(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	baseUrl string,
	timeout int,
) error {
	done := make(chan error, 1)
	go func() {
		done <- runOpenEnvShell(input, output, baseUrl, timeout)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		fmt.Fprintln(output)
		return nil
	}
}

func newRunCommand() *cobra.Command {
	flags := &openEnvInvokeFlags{}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Build and run the local RLE environment container",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stopSignals()

			baseUrl, err := ensureLocalContainerEndpoint(cmd, flags)
			if err != nil {
				return err
			}
			state, err := loadLocalRunState(flags)
			if err != nil {
				return err
			}
			defer func() {
				if err := stopLocalContainer(cmd, state.Name); err != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to stop local container: %v\n", err)
				}
			}()

			watchDone := make(chan error, 1)
			if flags.watch {
				watchCtx, cancelWatch := context.WithCancel(ctx)
				defer cancelWatch()
				watchCmd := *cmd
				watchCmd.SetContext(watchCtx)
				go func() {
					watchDone <- watchLocalContainer(&watchCmd, flags)
				}()
			}

			webUrl := baseUrl + "/web"
			_, err = fmt.Fprintf(
				cmd.OutOrStdout(),
				"Local RLE environment is running at %s\nPlayground UI: %s\n",
				baseUrl,
				webUrl,
			)
			if err != nil {
				return err
			}
			if err := openBrowserFunc(webUrl); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to open playground UI: %v\n", err)
			}
			shellErr := runOpenEnvShellWithContext(ctx, cmd.InOrStdin(), cmd.OutOrStdout(), baseUrl, 0)
			if flags.watch {
				select {
				case err := <-watchDone:
					if err != nil && shellErr == nil {
						return err
					}
				default:
				}
			}
			return shellErr
		},
	}

	cmd.Flags().IntVar(
		&flags.port,
		"port",
		0,
		"Host port mapped to the local Docker container. Defaults to 8000.",
	)
	cmd.Flags().StringVar(
		&flags.dockerfile,
		"dockerfile",
		"",
		"Dockerfile path relative to the current folder. Defaults to Dockerfile at the source root or server/Dockerfile.",
	)
	cmd.Flags().BoolVar(
		&flags.watch,
		"watch",
		false,
		"Watch source files and rebuild/restart the local container when they change.",
	)
	return cmd
}

const defaultPort = 8000

func normalizeOpenEnvOperation(operation string) (string, error) {
	operation = strings.Trim(strings.ToLower(operation), "/")
	switch operation {
	case "reset", "step", "state", "health", "metadata", "schema":
		return operation, nil
	default:
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Unknown OpenEnv operation %q.", operation),
			Code:       "rle_unknown_openenv_operation",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use one of: reset, step, state, health, metadata, schema.",
		}
	}
}

func runOpenEnvShell(input io.Reader, output io.Writer, baseUrl string, timeout int) error {
	fmt.Fprintln(output, "OpenEnv shell. Type help for commands, exit to quit.")
	scanner := bufio.NewScanner(input)
	for {
		fmt.Fprint(output, "rle> ")
		if !scanner.Scan() {
			fmt.Fprintln(output)
			return scanner.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "exit") || strings.EqualFold(line, "quit") {
			return nil
		}
		if strings.EqualFold(line, "help") {
			printOpenEnvShellHelp(output)
			continue
		}

		operation, payload, _ := strings.Cut(line, " ")
		operation, err := normalizeOpenEnvOperation(operation)
		if err != nil {
			fmt.Fprintf(output, "error: %v\n", err)
			continue
		}
		payload = strings.TrimSpace(payload)
		flags := &openEnvInvokeFlags{timeout: timeout}
		switch operation {
		case "reset":
			flags.body = payload
		case "step":
			if payload == "" {
				fmt.Fprintln(output, "error: step requires a JSON action payload, for example: step {\"message\":\"hello\"}")
				continue
			}
			flags.action = payload
		default:
			if payload != "" {
				fmt.Fprintf(output, "error: %s does not accept a JSON payload\n", operation)
				continue
			}
		}

		response, err := callOpenEnv(baseUrl, operation, flags)
		if err != nil {
			fmt.Fprintf(output, "error: %v\n", err)
			continue
		}
		fmt.Fprintln(output, response)
	}
}

func printOpenEnvShellHelp(output io.Writer) {
	fmt.Fprintln(output, "Commands:")
	fmt.Fprintln(output, "  health")
	fmt.Fprintln(output, "  reset [json]")
	fmt.Fprintln(output, "  step [json-action]")
	fmt.Fprintln(output, "  state")
	fmt.Fprintln(output, "  metadata")
	fmt.Fprintln(output, "  schema")
	fmt.Fprintln(output, "  exit")
}

func ensureLocalContainerEndpoint(cmd *cobra.Command, flags *openEnvInvokeFlags) (string, error) {
	state, err := loadLocalRunState(flags)
	if err != nil {
		return "", err
	}
	port := resolvePort(flags)
	if port <= 0 {
		return "", &azdext.LocalError{
			Message:    "--port must be greater than 0.",
			Code:       "rle_invalid_local_port",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Pass a valid host port, for example --port 8000.",
		}
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return "", &azdext.LocalError{
			Message:    "Could not find \"docker\" on PATH.",
			Code:       "rle_docker_not_found",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Install/start Docker Desktop, then retry the command.",
		}
	}

	image := localRuntimeImageForRun(flags, state)
	container := localContainerName(state.Name)
	baseUrl := fmt.Sprintf("http://localhost:%d", port)

	if running, exists := localContainerStatus(cmd, container); exists {
		if running && flags.reuseRunning && !flags.restart {
			if err := waitForOpenEnvHealth(baseUrl, 30*time.Second); err != nil {
				return "", err
			}
			return baseUrl, nil
		}
		_ = runDocker(cmd, "rm", "-f", container)
	}
	if err := ensurePortAvailable(port); err != nil {
		return "", err
	}
	if err := buildLocalRuntimeImage(cmd, image, dockerBuildOptions{
		source:     flags.source,
		dockerfile: flags.dockerfile,
	}); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintf(
		cmd.ErrOrStderr(),
		"Starting local container %s on port %d ...\n",
		container,
		port,
	); err != nil {
		return "", err
	}
	portMapping := fmt.Sprintf("%d:8000", port)
	runArgs := []string{
		"run", "-d",
		"--name", container,
		"--label", localContainerImageLabel + "=" + image,
		"-e", "ENABLE_WEB_INTERFACE=true",
		"-p", portMapping,
		image,
	}
	if err := runDocker(cmd, runArgs...); err != nil {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to start local Docker container %q: %v", container, err),
			Code:       "rle_local_docker_run_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: localPortSuggestion(port),
		}
	}
	started := true
	cleanupStartedContainer := func() {
		if started {
			_ = runDocker(cmd, "rm", "-f", container)
		}
	}

	if err := waitForOpenEnvHealth(baseUrl, 30*time.Second); err != nil {
		cleanupStartedContainer()
		return "", err
	}
	started = false
	return baseUrl, nil
}

func openBrowser(url string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "windows":
		command = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		command = "open"
		args = []string{url}
	default:
		command = "xdg-open"
		args = []string{url}
	}
	return exec.Command(command, args...).Start()
}

var openBrowserFunc = openBrowser

func ensurePortAvailable(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("Port %d is already in use.", port),
			Code:       "rle_local_port_in_use",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: localPortSuggestion(port),
		}
	}
	return listener.Close()
}

func localPortSuggestion(port int) string {
	return fmt.Sprintf(
		"Check containers using this port: docker ps --filter \"publish=%d\" --format \"table {{.Names}}\\t{{.Ports}}\"\n"+
			"Stop the container: docker rm -f <container>\n"+
			"Then rerun: azd ai rle run --port %d\n"+
			"If Docker does not show a container, check the process with: netstat -ano | findstr :%d",
		port,
		port,
		port,
	)
}

func localContainerAlreadyRunningError(_ string, port int) error {
	return &azdext.LocalError{
		Message:  fmt.Sprintf("A container is already running on port %d.", port),
		Code:     "rle_local_container_already_running",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf(
			"Check it with: docker ps --filter \"publish=%d\" --format \"table {{.Names}}\\t{{.Ports}}\"\n"+
				"Stop it with: docker rm -f <container>\n"+
				"Then rerun: azd ai rle run --port %d",
			port,
			port,
		),
	}
}

func stopLocalContainer(cmd *cobra.Command, environmentName string) error {
	container := localContainerName(environmentName)
	return runDocker(cmd, "rm", "-f", container)
}

func loadLocalRunState(flags *openEnvInvokeFlags) (rleState, error) {
	state, err := loadRleState()
	if err != nil {
		if localErr, ok := errors.AsType[*azdext.LocalError](err); !ok ||
			localErr.Code != "rle_project_not_initialized" {
			return rleState{}, err
		}
		state = defaultRleState(defaultSourceName(flags.source))
	}

	state.Name = firstNonEmpty(state.Name, defaultSourceName(flags.source))
	return state, nil
}

func localRuntimeImageForRun(flags *openEnvInvokeFlags, state rleState) string {
	return slug(firstNonEmpty(state.Name, defaultSourceName(flags.source))) + ":local"
}

func defaultSourceName(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "."
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return "rle_env"
	}
	name := filepath.Base(abs)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "rle_env"
	}
	return slug(name)
}

func buildLocalRuntimeImage(cmd *cobra.Command, image string, opts dockerBuildOptions) error {
	source, dockerfile, cleanup, err := prepareDockerBuild(opts)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	if _, err := fmt.Fprintf(
		cmd.ErrOrStderr(),
		"Building local runtime image %s from %s ...\n",
		image,
		dockerfile,
	); err != nil {
		return err
	}
	if err := runDocker(cmd, "build", "-t", image, "-f", dockerfile, source); err != nil {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to build Docker image %q: %v", image, err),
			Code:       "rle_local_docker_build_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Fix the Dockerfile or generated environment code, then retry.",
		}
	}
	return nil
}

func watchLocalContainer(cmd *cobra.Command, flags *openEnvInvokeFlags) error {
	last, err := sourceSnapshot(flags.source)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Watching for source changes. Press Ctrl+C to stop."); err != nil {
		return err
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-cmd.Context().Done():
			return nil
		case <-ticker.C:
			current, err := sourceSnapshot(flags.source)
			if err != nil {
				return err
			}
			if current == last {
				continue
			}
			last = current
			if _, err := fmt.Fprintln(
				cmd.OutOrStdout(),
				"Source change detected; rebuilding local container ...",
			); err != nil {
				return err
			}
			restartFlags := *flags
			restartFlags.restart = true
			baseUrl, err := ensureLocalContainerEndpoint(cmd, &restartFlags)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Local RLE environment restarted at %s\n", baseUrl); err != nil {
				return err
			}
		}
	}
}

func sourceSnapshot(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "."
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	var latest int64
	var count int
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && shouldSkipWatchDir(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		count++
		if modified := info.ModTime().UnixNano(); modified > latest {
			latest = modified
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", latest, count), nil
}

func shouldSkipWatchDir(name string) bool {
	switch name {
	case ".azd", ".git", ".venv", "__pycache__", "node_modules", "venv":
		return true
	default:
		return false
	}
}

func resolvePort(flags *openEnvInvokeFlags) int {
	if flags.port > 0 {
		return flags.port
	}
	return defaultPort
}

func localContainerStatus(cmd *cobra.Command, container string) (running bool, exists bool) {
	//nolint:gosec // Fixed docker inspect command; container is a generated local name.
	process := exec.CommandContext(cmd.Context(), "docker", "inspect", "-f", "{{.State.Running}}", container)
	process.Env = os.Environ()
	output, err := process.Output()
	if err != nil {
		return false, false
	}
	return strings.TrimSpace(string(output)) == "true", true
}

const localContainerImageLabel = "azd.ai.rle.local-image"

func runDocker(cmd *cobra.Command, args ...string) error {
	//nolint:gosec // Fixed docker command shapes with selected names/tags.
	process := exec.CommandContext(cmd.Context(), "docker", args...)
	process.Stdout = cmd.OutOrStdout()
	process.Stderr = cmd.ErrOrStderr()
	process.Env = os.Environ()
	return process.Run()
}

func localContainerName(envName string) string {
	return "azd-rle-" + slug(firstNonEmpty(envName, "environment"))
}

func waitForOpenEnvHealth(baseUrl string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(strings.TrimRight(baseUrl, "/") + "/health") //nolint:gosec // Local user-selected endpoint.
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			lastErr = fmt.Errorf("health returned HTTP %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(time.Second)
	}
	return &azdext.LocalError{
		Message:    fmt.Sprintf("OpenEnv endpoint did not become healthy at %s: %v", baseUrl, lastErr),
		Code:       "rle_local_container_not_ready",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Check the local container logs or remote sandbox status, then retry.",
	}
}

func callOpenEnv(baseUrl string, operation string, flags *openEnvInvokeFlags) (string, error) {
	method := http.MethodGet
	var body io.Reader
	if operation == "reset" || operation == "step" {
		method = http.MethodPost
		requestBody, err := openEnvRequestBody(operation, flags)
		if err != nil {
			return "", err
		}
		body = bytes.NewReader(requestBody)
	}

	url := strings.TrimRight(baseUrl, "/") + "/" + operation
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := openEnvHTTPClient(flags.timeout)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call OpenEnv %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &azdext.LocalError{
			Message:  fmt.Sprintf("OpenEnv endpoint returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data))),
			Code:     "rle_openenv_call_failed",
			Category: azdext.LocalErrorCategoryUser,
		}
	}

	return prettyJson(data), nil
}

func openEnvHTTPClient(timeoutSeconds int) *http.Client {
	if timeoutSeconds <= 0 {
		return &http.Client{}
	}
	return &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second}
}

func openEnvRequestBody(operation string, flags *openEnvInvokeFlags) ([]byte, error) {
	if strings.TrimSpace(flags.body) != "" {
		return validateJsonObject(flags.body, "body")
	}
	if operation == "reset" {
		return []byte("{}"), nil
	}

	action, err := validateJsonObject(flags.action, "action")
	if err != nil {
		return nil, err
	}
	var actionValue any
	if err := json.Unmarshal(action, &actionValue); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"action": actionValue})
}

func validateJsonObject(value string, flagName string) ([]byte, error) {
	var decoded map[string]any
	data := []byte(value)
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, &azdext.LocalError{
			Message:    fmt.Sprintf("--%s must be valid JSON object: %v", flagName, err),
			Code:       "rle_invalid_json",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: jsonFlagSuggestion(flagName),
		}
	}
	return data, nil
}

func jsonFlagSuggestion(flagName string) string {
	if flagName == "body" {
		return "Use reset {\"seed\":0} or another JSON object."
	}
	return "Use step {\"message\":\"hello\"} or another JSON object."
}

func prettyJson(data []byte) string {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return string(data)
	}
	formatted, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		return string(data)
	}
	return string(formatted)
}
