// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/gofrs/flock"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

const (
	codeInvalidOptions = "invalid_options"
	codeLicenseNeeded  = "license_not_accepted"
	codeNoInstance     = "instance_not_found"
)

var emulatorName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Manager runs isolated Compose projects and preserves their connection settings.
type Manager struct {
	runner        exec.CommandRunner
	stateDir      string
	client        *http.Client
	projectScoped bool
}

type savedState struct {
	Version     int     `json:"version"`
	Emulator    string  `json:"emulator"`
	ProjectName string  `json:"projectName"`
	Options     Options `json:"options"`
	Spec        Spec    `json:"spec"`
}

// NewManager creates a launcher using the supplied command runner and private state directory.
func NewManager(runner exec.CommandRunner, stateDir string) *Manager {
	return &Manager{
		runner: runner, stateDir: stateDir,
		client: &http.Client{
			Timeout: 2 * time.Second,
			// Emulator health checks must never leave the local host through a proxy or redirect.
			Transport: &http.Transport{Proxy: nil},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// ForProject returns a manager whose state is isolated from standalone instances and other environments.
func (m *Manager) ForProject(projectDir, environment string) *Manager {
	scope := sha256.Sum256([]byte(projectDir + "\x00" + environment))
	project := NewManager(m.runner, filepath.Join(m.stateDir, "projects", fmt.Sprintf("%x", scope)))
	project.projectScoped = true
	return project
}

func (m *Manager) stopCommand(name string) string {
	if m.projectScoped {
		return "azd down"
	}
	return "azd emulators " + name + " --stop"
}

// IsInstanceMissing reports whether the emulator has no saved instance in this scope.
func IsInstanceMissing(err error) bool {
	local, ok := errors.AsType[*azdext.LocalError](err)
	return ok && local.Code == codeNoInstance
}

// Run starts or stops the requested emulator and returns its verified connection information.
func (m *Manager) Run(
	ctx context.Context, definition Definition, request Request,
) (result Result, returnErr error) {
	if !emulatorName.MatchString(definition.Name) {
		return Result{}, fmt.Errorf("invalid emulator name %q", definition.Name)
	}
	if err := ValidateRequest(definition, request); err != nil {
		return Result{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()

	if err := os.MkdirAll(m.stateDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create emulator state directory: %w", err)
	}
	lock := flock.New(filepath.Join(m.stateDir, definition.Name+".lock"))
	locked, err := lock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return Result{}, fmt.Errorf("lock emulator state: %w", err)
	}
	if !locked {
		return Result{}, fmt.Errorf("waiting for another %s command: %w", definition.Name, ctx.Err())
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Close()) }()

	statePath := filepath.Join(m.stateDir, definition.Name+".json")
	state, err := m.loadState(statePath, definition.Name)
	if err != nil {
		return Result{}, err
	}

	if request.Stop {
		if state == nil {
			return Result{}, validation(codeNoInstance, "No saved "+definition.DisplayName+" instance exists.",
				"Start it with azd emulators "+definition.Name+".")
		}
		if _, err := m.CheckDocker(ctx); err != nil {
			return Result{}, err
		}
		if _, err := m.compose(ctx, state, []string{"down", "--volumes"}); err != nil {
			return Result{}, err
		}
		if err := os.Remove(statePath); err != nil {
			return Result{}, fmt.Errorf("remove stopped emulator state: %w", err)
		}
		names := make([]string, 0, len(state.Spec.Connections))
		for _, connection := range state.Spec.Connections {
			names = append(names, connection.Name)
		}
		return Result{
			Emulator: definition.Name, Status: "stopped", ProjectName: state.ProjectName,
			RemovedConnectionNames: names,
		}, nil
	}

	if state != nil {
		if err := checkChangedOptions(state.Options, request); err != nil {
			return Result{}, err
		}
	} else {
		if len(definition.LicenseURLs) > 0 && !request.AcceptLicense {
			return Result{}, validation(codeLicenseNeeded,
				"Starting "+definition.DisplayName+" requires acceptance of its license terms.",
				"Review "+strings.Join(definition.LicenseURLs, ", ")+
					" and run again with --accept-license.")
		}
		spec, err := definition.Build(request.Options)
		if err != nil {
			return Result{}, err
		}
		state = &savedState{
			Version: 1, Emulator: definition.Name, ProjectName: m.projectName(definition.Name),
			Options: request.Options, Spec: spec,
		}
	}

	if err := m.CheckRequirements(ctx, definition); err != nil {
		return Result{}, err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("encode emulator state: %w", err)
	}
	if state.Spec.Dockerfile != "" {
		// Only the Dockerfile belongs in the build context, never state files or credentials.
		directory := filepath.Join(m.stateDir, "build", definition.Name)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return Result{}, fmt.Errorf("create image build context: %w", err)
		}
		if err := azdext.WriteFileAtomic(
			filepath.Join(directory, "Dockerfile"), []byte(state.Spec.Dockerfile), 0o600,
		); err != nil {
			return Result{}, fmt.Errorf("write emulator Dockerfile: %w", err)
		}
	}
	// Preserve enough information to stop partial launches and reuse generated passwords.
	if err := azdext.WriteFileAtomic(statePath, data, 0o600); err != nil {
		return Result{}, fmt.Errorf("save emulator state: %w", err)
	}
	if _, err := m.compose(ctx, state, []string{"up", "--detach"}); err != nil {
		return Result{}, fmt.Errorf("%w\nClean up with %s.", err, m.stopCommand(definition.Name))
	}
	if err := m.waitReady(ctx, state); err != nil {
		return Result{}, fmt.Errorf(
			"%w\nInspect containers with docker ps -a --filter label=com.docker.compose.project=%s."+
				"\nClean up with %s.", err, state.ProjectName, m.stopCommand(definition.Name))
	}
	return Result{
		Emulator: definition.Name, Status: "running", ProjectName: state.ProjectName,
		Connections: state.Spec.Connections, Notes: state.Spec.Notes,
	}, nil
}

// ValidateRequest rejects invalid ports, timeouts and explicitly conflicting flags.
func ValidateRequest(definition Definition, request Request) error {
	if request.Changed["environment"] {
		return validation(codeInvalidOptions,
			"--environment is not supported. Emulator instances are shared across environments.",
			"Omit --environment. Use a separate azd configuration directory when you need isolated instances.")
	}
	if request.Timeout <= 0 {
		return validation(codeInvalidOptions, "--timeout must be greater than zero.", "Use --timeout 5m.")
	}
	if request.Stop {
		for _, flag := range slices.Sorted(maps.Keys(request.Changed)) {
			if request.Changed[flag] && flag != "stop" && flag != "timeout" {
				return validation(codeInvalidOptions, "--"+flag+" cannot be used with --stop.",
					"Use --stop alone, then supply launch options when starting the emulator.")
			}
		}
		return nil
	}
	used := map[int]string{}
	for _, port := range definition.Ports {
		value := request.Options.Ports[port.Flag]
		if value < 1 || value > 65535 {
			return validation(codeInvalidOptions, "--"+port.Flag+" must be between 1 and 65535.",
				"Choose an unused local port.")
		}
		if previous, exists := used[value]; exists {
			return validation(codeInvalidOptions, "--"+port.Flag+" conflicts with --"+previous+".",
				"Assign a different local port to each endpoint.")
		}
		used[value] = port.Flag
	}
	return nil
}

func checkChangedOptions(saved Options, request Request) error {
	for flag, port := range request.Options.Ports {
		if request.Changed[flag] && saved.Ports[flag] != port {
			return changedOption(flag)
		}
	}
	if request.Changed["directory"] && saved.Directory != request.Options.Directory {
		return changedOption("directory")
	}
	if request.Changed["upstream-url"] && saved.UpstreamURL != request.Options.UpstreamURL {
		return changedOption("upstream-url")
	}
	return nil
}

func changedOption(flag string) error {
	return validation(codeInvalidOptions, "--"+flag+" differs from the saved instance configuration.",
		"Stop the emulator with --stop before changing its launch options.")
}

func validation(code, message, suggestion string) error {
	return &azdext.LocalError{
		Code: code, Category: azdext.LocalErrorCategoryValidation, Message: message, Suggestion: suggestion,
	}
}

func (m *Manager) projectName(name string) string {
	sum := sha256.Sum256([]byte(m.stateDir))
	return fmt.Sprintf("azd-emulators-%x-%s", sum[:6], name)
}

func (m *Manager) loadState(path, name string) (*savedState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read emulator state: %w", err)
	}
	var state savedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode emulator state %q: %w", path, err)
	}
	if state.Version != 1 || state.Emulator != name || state.ProjectName != m.projectName(name) ||
		len(state.Spec.Compose.Services) == 0 || (len(state.Spec.Probes) == 0 && len(state.Spec.ExecProbe) == 0) {
		return nil, fmt.Errorf("invalid emulator state in %q", path)
	}
	for _, service := range state.Spec.Compose.Services {
		if service.Build != nil &&
			(service.Build.Context != "build/"+name || state.Spec.Dockerfile == "") {
			return nil, fmt.Errorf("invalid emulator build context in %q", path)
		}
	}
	return &state, nil
}

// CheckRequirements checks the local Docker prerequisites for an emulator without installing anything.
func (m *Manager) CheckRequirements(ctx context.Context, definition Definition) error {
	host, err := m.CheckDocker(ctx)
	if err != nil {
		return err
	}
	return host.CheckRequirements(definition)
}

// DockerHost describes the Docker daemon used to run emulators.
type DockerHost struct {
	Architecture string
}

// CheckRequirements checks whether the daemon architecture supports the emulator.
func (h DockerHost) CheckRequirements(definition Definition) error {
	if definition.RequiresAMD64 && h.Architecture != "amd64" && h.Architecture != "x86_64" {
		return validation(codeInvalidOptions, definition.DisplayName+" requires an AMD64 Docker host.",
			"Switch to a Docker context with an AMD64 daemon.")
	}
	return nil
}

// CheckDocker checks the shared Docker prerequisites without installing anything.
func (m *Manager) CheckDocker(ctx context.Context) (DockerHost, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := m.runner.ToolInPath("docker"); err != nil {
		return DockerHost{}, &azdext.ToolError{
			Message: "Docker is required to run emulators.", Err: err, ToolName: "docker",
			Kind: azdext.ToolErrorKindMissing, Suggestion: "Install Docker with Compose 2.23.1 or later.",
		}
	}
	version, err := m.runDocker(ctx, []string{"compose", "version", "--short"}, nil, nil)
	if err != nil {
		if ctx.Err() != nil {
			return DockerHost{}, fmt.Errorf("checking Docker Compose: %w", ctx.Err())
		}
		return DockerHost{}, &azdext.ToolError{
			Message:    "Docker Compose 2.23.1 or later is required. `docker compose` is unavailable.",
			Err:        err,
			ToolName:   "docker compose",
			Kind:       azdext.ToolErrorKindFailed,
			Suggestion: "Install or repair the Docker Compose plugin, then run `docker compose version` to verify it.",
		}
	}
	parsed, err := semver.ParseTolerant(strings.TrimSpace(version))
	if err != nil || parsed.LT(semver.MustParse("2.23.1")) {
		return DockerHost{}, validation(codeInvalidOptions, "Docker Compose 2.23.1 or later is required.",
			"Update the Docker Compose plugin.")
	}
	info, err := m.runDocker(ctx, []string{"info", "--format", "{{.OSType}} {{.Architecture}}"}, nil, nil)
	if err != nil {
		return DockerHost{}, &azdext.ToolError{
			Message: "Docker daemon is unavailable.", Err: err, ToolName: "docker",
			Kind: azdext.ToolErrorKindFailed, Suggestion: "Start Docker or switch to a working Docker context.",
		}
	}
	fields := strings.Fields(info)
	if len(fields) != 2 {
		return DockerHost{}, errors.New("could not determine the Docker daemon OS and architecture")
	}
	if fields[0] != "linux" {
		return DockerHost{}, validation(codeInvalidOptions, "Emulators require Linux containers.",
			"Switch Docker to Linux containers.")
	}
	return DockerHost{Architecture: fields[1]}, nil
}

func (m *Manager) compose(ctx context.Context, state *savedState, args []string) (string, error) {
	data, err := json.Marshal(state.Spec.Compose)
	if err != nil {
		return "", fmt.Errorf("encode Compose configuration: %w", err)
	}
	// All values are literal configuration, not references to the caller's environment.
	data = bytes.ReplaceAll(data, []byte("$"), []byte("$$"))
	command := []string{"compose", "--project-name", state.ProjectName, "--project-directory", m.stateDir, "-f", "-"}
	command = append(command, args...)
	output, err := m.runDocker(ctx, command, data, state.Spec.Secrets)
	if toolErr, ok := errors.AsType[*azdext.ToolError](err); ok {
		toolErr.Suggestion += " Clean up this instance with " + m.stopCommand(state.Emulator) + "."
	}
	return output, err
}

func (m *Manager) runDocker(
	ctx context.Context, args []string, input []byte, secrets []string,
) (string, error) {
	result, err := m.runner.Run(ctx, exec.RunArgs{
		Cmd: "docker", Args: args, StdIn: bytes.NewReader(input), DebugLogging: new(false),
	})
	if err != nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = err.Error()
		}
		return "", &azdext.ToolError{
			Message: "Docker failed: " + sanitizeDiagnostic(message, secrets), Err: err,
			ToolName: "docker", Kind: azdext.ToolErrorKindFailed, ExitCode: new(result.ExitCode),
			Suggestion: "Check that Docker is running, accessible, and includes the Compose plugin.",
		}
	}
	return result.Stdout, nil
}

func (m *Manager) waitReady(ctx context.Context, state *savedState) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastErr error
	for {
		running, err := m.checkContainers(ctx, state)
		if err != nil {
			return err
		}
		lastErr = fmt.Errorf("waiting for all emulator containers to run")
		if running {
			lastErr = m.checkEndpoints(ctx, state)
		}
		if lastErr == nil && len(state.Spec.ExecProbe) > 0 {
			_, lastErr = m.compose(ctx, state, append([]string{"exec", "-T", "emulator"}, state.Spec.ExecProbe...))
		}
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s did not become ready: %w: %w", state.Emulator, ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func (m *Manager) checkContainers(ctx context.Context, state *savedState) (bool, error) {
	output, err := m.compose(ctx, state, []string{"ps", "--all", "--format", "json"})
	if err != nil {
		return false, err
	}
	statuses, err := parseContainerStatuses(output)
	if err != nil {
		return false, err
	}
	running := make(map[string]bool)
	for _, status := range statuses {
		if status.State == "exited" || status.State == "dead" {
			return false, fmt.Errorf("%s container %s stopped with exit code %d",
				state.Emulator, status.Service, status.ExitCode)
		}
		if status.State == "running" {
			running[status.Service] = true
		}
	}
	for service := range state.Spec.Compose.Services {
		if !running[service] {
			return false, nil
		}
	}
	return true, nil
}

type containerStatus struct {
	Service  string
	State    string
	ExitCode int
}

func parseContainerStatuses(output string) ([]containerStatus, error) {
	var statuses []containerStatus
	output = strings.TrimSpace(output)
	if strings.HasPrefix(output, "[") {
		if err := json.Unmarshal([]byte(output), &statuses); err != nil {
			return nil, fmt.Errorf("decode Docker container status: %w", err)
		}
		return statuses, nil
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	for {
		var status containerStatus
		if err := decoder.Decode(&status); err != nil {
			if errors.Is(err, io.EOF) {
				return statuses, nil
			}
			return nil, fmt.Errorf("decode Docker container status: %w", err)
		}
		statuses = append(statuses, status)
	}
}

func (m *Manager) checkEndpoints(ctx context.Context, state *savedState) error {
	for _, port := range state.Options.Ports {
		dialer := net.Dialer{Timeout: time.Second}
		connection, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return fmt.Errorf("waiting for port %d: %w", port, err)
		}
		if err := connection.Close(); err != nil {
			return err
		}
	}
	for _, probe := range state.Spec.Probes {
		method := probe.Method
		if method == "" {
			method = http.MethodGet
		}
		request, err := http.NewRequestWithContext(ctx, method, probe.URL, strings.NewReader(probe.Body))
		if err != nil {
			return fmt.Errorf("create readiness request: %w", err)
		}
		if probe.Body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := m.client.Do(request)
		if err != nil {
			return fmt.Errorf("readiness request: %w", err)
		}
		if err := response.Body.Close(); err != nil {
			return err
		}
		if response.StatusCode != probe.Status {
			return fmt.Errorf("readiness check returned HTTP %d, expected %d", response.StatusCode, probe.Status)
		}
	}
	return nil
}
