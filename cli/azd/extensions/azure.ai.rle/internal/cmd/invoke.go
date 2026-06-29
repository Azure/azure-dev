// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore openenv

package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type openEnvInvokeFlags struct {
	local       bool
	port        int
	persistPort bool
	timeout     int
	action      string
	body        string
}

func newInvokeCommand() *cobra.Command {
	flags := &openEnvInvokeFlags{
		timeout: 30,
	}

	cmd := &cobra.Command{
		Use:   "invoke",
		Short: "Open an OpenEnv runtime shell",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !flags.local {
				return &azdext.LocalError{
					Message:    "Remote invoke is not supported yet.",
					Code:       "rle_remote_invoke_not_supported",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Pass --local to invoke the local OpenEnv runtime.",
				}
			}
			baseUrl, err := ensureLocalContainerEndpoint(cmd, flags)
			if err != nil {
				return err
			}
			return runOpenEnvShell(cmd.InOrStdin(), cmd.OutOrStdout(), baseUrl, flags.timeout)
		},
	}

	cmd.Flags().BoolVar(
		&flags.local,
		"local",
		false,
		"Run the configured local Docker image if needed, then call that container.",
	)
	cmd.Flags().IntVar(
		&flags.timeout,
		"timeout",
		flags.timeout,
		"Per-command OpenEnv request timeout in seconds (0 for no timeout).",
	)
	return cmd
}

func newRunCommand() *cobra.Command {
	flags := &openEnvInvokeFlags{
		persistPort: true,
	}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Pull and run the local RLE environment container",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			baseUrl, err := ensureLocalContainerEndpoint(cmd, flags)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Local RLE environment is running at %s\n", baseUrl)
			return err
		},
	}

	cmd.Flags().IntVar(
		&flags.port,
		"port",
		0,
		"Host port mapped to the local Docker container. Defaults to the saved port or 8000.",
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
	state, err := loadRleState()
	if err != nil {
		return "", err
	}
	port := resolvePort(flags, state)
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

	image, err := localRuntimeImage(state)
	if err != nil {
		return "", err
	}
	container := localContainerName(state.Name)
	baseUrl := fmt.Sprintf("http://localhost:%d", port)
	running, exists := localContainerStatus(cmd, container)
	switch {
	case running:
		if !shouldRecreateContainer(cmd, container, port, image) {
			if err := waitForOpenEnvHealth(baseUrl, 30*time.Second); err != nil {
				return "", err
			}
			if err := savePortIfRequested(flags, state, port); err != nil {
				return "", err
			}
			return baseUrl, nil
		}
	case exists:
		if !shouldRecreateContainer(cmd, container, port, image) {
			if _, err := fmt.Fprintf(
				cmd.ErrOrStderr(),
				"Starting existing local container %s ...\n",
				container,
			); err != nil {
				return "", err
			}
			if err := runDocker(cmd, "start", container); err == nil {
				if err := waitForOpenEnvHealth(baseUrl, 30*time.Second); err != nil {
					return "", err
				}
				if err := savePortIfRequested(flags, state, port); err != nil {
					return "", err
				}
				return baseUrl, nil
			}
			if _, err := fmt.Fprintf(
				cmd.ErrOrStderr(),
				"Existing local container %s did not start; recreating it ...\n",
				container,
			); err != nil {
				return "", err
			}
		}
	}

	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Pulling local runtime image %s ...\n", image); err != nil {
		return "", err
	}
	if err := runDocker(cmd, "pull", image); err != nil {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to pull Docker image %q: %v", image, err),
			Code:       "rle_local_docker_pull_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: dockerPullFailureSuggestion(image),
		}
	}

	if _, exists := localContainerStatus(cmd, container); exists {
		_ = runDocker(cmd, "rm", "-f", container)
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
	if err := runDocker(cmd,
		"run", "-d",
		"--name", container,
		"--label", localContainerImageLabel+"="+image,
		"-p", portMapping,
		image); err != nil {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to start local Docker container %q: %v", container, err),
			Code:       "rle_local_docker_run_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Ensure the selected --port is free, or pass a different port.",
		}
	}

	if err := waitForOpenEnvHealth(baseUrl, 30*time.Second); err != nil {
		return "", err
	}
	if err := savePortIfRequested(flags, state, port); err != nil {
		return "", err
	}
	return baseUrl, nil
}

func resolvePort(flags *openEnvInvokeFlags, state rleState) int {
	if flags.port > 0 {
		return flags.port
	}
	if state.Port > 0 {
		return state.Port
	}
	return defaultPort
}

func savePortIfRequested(flags *openEnvInvokeFlags, state rleState, port int) error {
	if !flags.persistPort || state.Port == port {
		return nil
	}
	state.Port = port
	return saveRleState(state)
}

func localRuntimeImage(state rleState) (string, error) {
	manifest, manifestErr := loadRleManifest(rleManifestFile)
	if manifestErr == nil {
		image := localImageFromManifest(manifest)
		if strings.TrimSpace(image) != "" {
			return strings.TrimSpace(image), nil
		}
		return "", missingLocalRuntimeImageError()
	} else if !errors.Is(manifestErr, os.ErrNotExist) {
		return "", manifestErr
	}
	if strings.TrimSpace(state.LocalImage) != "" {
		return strings.TrimSpace(state.LocalImage), nil
	}
	if strings.TrimSpace(state.Image) != "" {
		return strings.TrimSpace(state.Image), nil
	}
	return "", missingLocalRuntimeImageError()
}

func missingLocalRuntimeImageError() error {
	return &azdext.LocalError{
		Message:    "No local runtime image is configured.",
		Code:       "rle_local_image_required",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Set template.local.image or template.environment.image in rle.yaml, then rerun the command.",
	}
}

func dockerPullFailureSuggestion(image string) string {
	if registry, ok := acrRegistryName(image); ok {
		return fmt.Sprintf(
			"Run az acr login --name %s, or set template.local.image in %s to an image available to Docker.",
			registry,
			rleManifestFile,
		)
	}
	return fmt.Sprintf(
		"Sign in to the image registry if required, or set template.local.image in %s to an image available to Docker.",
		rleManifestFile,
	)
}

func acrRegistryName(image string) (string, bool) {
	host := strings.ToLower(strings.TrimSpace(strings.SplitN(image, "/", 2)[0]))
	const suffix = ".azurecr.io"
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	registry := strings.TrimSuffix(host, suffix)
	if registry == "" {
		return "", false
	}
	return registry, true
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

func shouldRecreateContainer(cmd *cobra.Command, container string, requestedPort int, expectedImage string) bool {
	actualPort, ok := localContainerHostPort(cmd, container)
	if !ok {
		_, _ = fmt.Fprintf(
			cmd.ErrOrStderr(),
			"Existing local container %s has no usable port mapping; recreating it ...\n",
			container,
		)
		return true
	}
	if actualPort != requestedPort {
		_, _ = fmt.Fprintf(
			cmd.ErrOrStderr(),
			"Existing local container %s uses port %d; recreating it on port %d ...\n",
			container,
			actualPort,
			requestedPort,
		)
		return true
	}

	actualImage, ok := localContainerExpectedImage(cmd, container)
	if ok && actualImage == expectedImage {
		return false
	}
	_, _ = fmt.Fprintf(
		cmd.ErrOrStderr(),
		"Existing local container %s was created for a different image; recreating it ...\n",
		container,
	)
	return true
}

func localContainerHostPort(cmd *cobra.Command, container string) (int, bool) {
	//nolint:gosec // Fixed docker inspect command; container is a generated local name.
	process := exec.CommandContext(
		cmd.Context(),
		"docker",
		"inspect",
		"-f",
		`{{(index (index .NetworkSettings.Ports "8000/tcp") 0).HostPort}}`,
		container,
	)
	process.Env = os.Environ()
	output, err := process.Output()
	if err != nil {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, false
	}
	return port, true
}

func localContainerExpectedImage(cmd *cobra.Command, container string) (string, bool) {
	//nolint:gosec // Fixed docker inspect command; container is a generated local name.
	process := exec.CommandContext(
		cmd.Context(),
		"docker",
		"inspect",
		"-f",
		"{{ index .Config.Labels \""+localContainerImageLabel+"\" }}",
		container,
	)
	process.Env = os.Environ()
	output, err := process.Output()
	if err != nil {
		return "", false
	}
	image := strings.TrimSpace(string(output))
	if image == "" || image == "<no value>" {
		return "", false
	}
	return image, true
}

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
		Message:    fmt.Sprintf("Local OpenEnv container did not become healthy at %s: %v", baseUrl, lastErr),
		Code:       "rle_local_container_not_ready",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Check docker logs for the generated container, then retry.",
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
