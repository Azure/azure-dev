// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	agentInspectorExtensionID     = "azure.ai.inspector"
	agentInspectorReadyPollPeriod = 250 * time.Millisecond
)

type runFlags struct {
	port         int
	name         string
	startCommand string
	noInspector  bool
}

func newRunCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &runFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "run [name]",
		Short: "Run your agent locally for development.",
		Long: `Run your agent locally for development.

Detects the project type (Python, .NET, Node.js), installs dependencies,
and starts the agent server in the foreground. Press Ctrl+C to stop.

Optionally specify the agent service name (from azure.yaml) as a
positional argument. When omitted, the single agent service is used.

The startup command is read from the startupCommand property of the
agent service in azure.yaml. If not set, it is auto-detected from the
project type. Use --start-command to override both.

By default, this also opens Agent Inspector after the local agent starts
listening. Use --no-inspector to skip this.`,
		Example: `  # Start the agent in the current directory
  azd ai agent run

  # Start a specific agent by name
  azd ai agent run my-agent

  # Start on a custom port
  azd ai agent run --port 9090

  # Start without opening Agent Inspector
  azd ai agent run --no-inspector

  # Start with an explicit command
  azd ai agent run --start-command "python app.py"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}
			ctx := azdext.WithAccessToken(cmd.Context())
			return runRun(ctx, flags, extCtx.NoPrompt)
		},
	}

	cmd.Flags().IntVarP(&flags.port, "port", "p", DefaultPort, "Port to listen on")
	cmd.Flags().StringVarP(&flags.startCommand, "start-command", "c", "",
		"Explicit startup command (overrides azure.yaml and auto-detection)")
	cmd.Flags().BoolVar(&flags.noInspector, "no-inspector", false, "Do not open Agent Inspector")

	return cmd
}

func runRun(ctx context.Context, flags *runFlags, noPrompt bool) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	// Resolve the service source directory and startup command from azure.yaml
	runCtx, err := resolveServiceRunContext(ctx, azdClient, flags.name, noPrompt)
	if err != nil {
		return err
	}
	projectDir := runCtx.ProjectDir

	// Clean up stored local session when the agent process exits.
	localAgentKey := resolveLocalAgentKeyWithPort(ctx, azdClient, runCtx.ServiceName, noPrompt, flags.port)
	defer func() {
		if err := deleteContextValue(ctx, azdClient, "sessions", localAgentKey); err != nil {
			log.Printf("run: failed to clear stored local session: %v", err)
		}
	}()

	// Detect project type early — used for both start-command resolution and
	// environment setup (e.g., setting ASPNETCORE_URLS for .NET).
	pt := detectProjectType(projectDir)

	// Resolve start command: --start-command flag > azure.yaml startupCommand > detect
	startCmd := flags.startCommand
	if startCmd == "" {
		startCmd = runCtx.StartupCommand
	}

	if startCmd == "" {
		if pt.StartCmd != "" {
			startCmd = pt.StartCmd
			fmt.Printf("Detected %s project. Start command: %s\n", pt.Language, startCmd)
		} else if pt.Language != "unknown" {
			return fmt.Errorf(
				"detected %s project in %s but could not determine the entry point\n\n"+
					"Use --start-command to specify explicitly, or set startupCommand in azure.yaml",
				pt.Language, projectDir,
			)
		} else {
			return fmt.Errorf(
				"could not detect project type in %s\n\n"+
					"Supported project types:\n"+
					"  - Python (pyproject.toml or requirements.txt with main.py)\n"+
					"  - .NET (*.csproj)\n"+
					"  - Node.js (package.json)\n\n"+
					"Use --start-command to specify explicitly, or set startupCommand in azure.yaml",
				projectDir,
			)
		}
	} else {
		fmt.Printf("Using startup command: %s\n", startCmd)
	}

	// Install dependencies
	if err := installDependencies(projectDir); err != nil {
		return fmt.Errorf("failed to install dependencies: %w", err)
	}

	// Build the command
	cmdParts := parseCommand(startCmd)
	if len(cmdParts) == 0 {
		return fmt.Errorf("empty start command")
	}

	cmdParts = resolveVenvCommand(projectDir, cmdParts)

	env := os.Environ()
	env = appendPortEnvVars(env, pt, flags.port)

	// Load azd environment variables (e.g., FOUNDRY_PROJECT_ENDPOINT)
	// so the agent can reach Azure services during local development.
	// Also translate azd env keys to FOUNDRY_* env vars so the agent code
	// works identically whether running locally or in a hosted container
	// (where the platform automatically injects FOUNDRY_* env vars).
	var azdEnvVars map[string]string
	if loaded, err := loadAzdEnvironment(ctx, azdClient); err == nil {
		azdEnvVars = loaded
		for k, v := range azdEnvVars {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		env = appendFoundryEnvVars(env, azdEnvVars, runCtx.ServiceName)
	} else if shouldWarnLoadAzdEnvironmentFailure(err) {
		fmt.Fprintf(os.Stderr, "Warning: failed to load azd environment values: %s\n", err)
	}

	// Resolve environment_variables from the agent definition (agent.yaml).
	// This handles hardcoded values, ${VAR} references (resolved via azd env),
	// and ${{connections.<name>.credentials.<key>}} references (resolved via
	// the Foundry data plane). Agent definition env vars do not override
	// values already present in the process environment.
	endpoint, _ := resolveAgentEndpoint(ctx, "", "")
	defEnv, defErr := resolveAgentDefinitionEnvVars(ctx, projectDir, azdEnvVars, endpoint)
	if defErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", defErr)
	}
	for _, entry := range defEnv {
		key, _, _ := strings.Cut(entry, "=")
		if !envSliceHasKey(env, key) {
			env = append(env, entry)
		}
	}

	url := fmt.Sprintf("http://localhost:%d", flags.port)

	// `run` holds the foreground TTY for the agent process and the
	// `Next:` block is a "wait + new terminal" sequence. Emitting it
	// before the agent has actually bound its port produces the
	// well-known race where a user alt-tabs to a fresh terminal and
	// pastes the suggested invoke before the server is up — and the
	// invoke fails. Defer the emission until net.DialTimeout against
	// localhost:port succeeds (or the budget elapses). See B5 in the
	// PR-8057 design spec.
	fmt.Printf("Starting agent on %s (Ctrl+C to stop)\n\n", url)

	// Create command with stdout/stderr piped to terminal
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	proc := exec.CommandContext(ctx, cmdParts[0], cmdParts[1:]...) //nolint:gosec // G204: startup command is from azure.yaml config or --start-command flag
	proc.Dir = projectDir
	proc.Env = env
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	proc.Stdin = os.Stdin

	if err := proc.Start(); err != nil {
		return fmt.Errorf("failed to start agent: %w", err)
	}

	inspectorInstalled := false
	var inspectorInstallErr error
	if !flags.noInspector {
		inspectorInstalled, inspectorInstallErr = isInspectorExtensionInstalled(ctx, azdClient)
	}
	handleInspectorAutoLaunch(
		ctx,
		azdClient.Workflow(),
		flags.port,
		flags.noInspector,
		inspectorInstalled,
		inspectorInstallErr,
		os.Stderr,
	)

	// Emit the `Next:` block once the agent's port is open. We don't
	// want users alt-tabbing to a fresh terminal and pasting the
	// suggested invoke before the server is ready to answer. The
	// goroutine returns silently if the agent never binds within the
	// budget (e.g., the process exited during boot — the user already
	// sees the stderr trace) or if the parent ctx is cancelled.
	// nextDone signals the goroutine has exited so runRun can join it
	// after proc.Wait returns, preventing stdout races on shutdown.
	nextDone := make(chan struct{})
	go func() {
		defer close(nextDone)
		emitNextAfterBind(ctx, azdClient, runCtx.ServiceName, flags.port)
	}()

	// Handle Ctrl+C / SIGTERM: forward signal to child, then wait for it to exit.
	// The done channel is closed after proc.Wait returns so the goroutine can exit.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		defer signal.Stop(sigCh)
		select {
		case <-sigCh:
			fmt.Println("\nStopping agent...")
			cancel()
		case <-done:
		}
	}()

	err = proc.Wait()
	close(done)
	cancel()
	<-nextDone

	// Suppress the noisy "signal: interrupt" error on Ctrl+C
	if ctx.Err() != nil {
		fmt.Println("Agent stopped.")
		return nil
	}

	if err != nil {
		return fmt.Errorf("agent exited: %w", err)
	}
	return nil
}

func handleInspectorAutoLaunch(
	ctx context.Context,
	workflow azdext.WorkflowServiceClient,
	agentPort int,
	noInspector bool,
	inspectorInstalled bool,
	inspectorInstallErr error,
	stderr io.Writer,
) {
	if noInspector {
		return
	}
	if inspectorInstallErr != nil {
		fmt.Fprintf(stderr, "Warning: Agent Inspector was not launched: %v\n", inspectorInstallErr)
		return
	}
	if !inspectorInstalled {
		fmt.Fprintln(stderr, missingInspectorExtensionWarning())
		return
	}
	startInspectorAfterAgentReadyWithOptions(
		ctx,
		workflow,
		agentPort,
		agentInspectorReadyPollPeriod,
		stderr,
	)
}

func startInspectorAfterAgentReadyWithOptions(
	ctx context.Context,
	workflow azdext.WorkflowServiceClient,
	agentPort int,
	pollPeriod time.Duration,
	stderr io.Writer,
) {
	go func() {
		if err := waitForLocalPort(ctx, agentPort, pollPeriod); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(
					stderr,
					"Warning: Agent Inspector was not launched because localhost:%d was not ready: %v\n",
					agentPort,
					err,
				)
			}
			return
		}

		if err := launchInspector(ctx, workflow, agentPort); err != nil && !isContextCancellation(err) {
			fmt.Fprintln(stderr, inspectorLaunchWarning(err))
		}
	}()
}

func waitForLocalPort(ctx context.Context, port int, pollPeriod time.Duration) error {
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	dialer := net.Dialer{Timeout: pollPeriod}
	ticker := time.NewTicker(pollPeriod)
	defer ticker.Stop()

	for {
		conn, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			_ = conn.Close()
			return nil
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("timed out waiting for %s to accept connections", address)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func launchInspector(ctx context.Context, workflow azdext.WorkflowServiceClient, agentPort int) error {
	_, err := workflow.Run(ctx, &azdext.RunWorkflowRequest{
		Workflow: &azdext.Workflow{
			Name: "launch-agent-inspector",
			Steps: []*azdext.WorkflowStep{
				{
					Command: &azdext.WorkflowCommand{
						Args: []string{
							"ai",
							"inspector",
							"launch",
							"--port",
							strconv.Itoa(agentPort),
							"--silent",
						},
					},
				},
			},
		},
	})
	return err
}

func isInspectorExtensionInstalled(ctx context.Context, azdClient *azdext.AzdClient) (bool, error) {
	configHelper, err := azdext.NewConfigHelper(azdClient)
	if err != nil {
		return false, err
	}

	var installed map[string]json.RawMessage
	found, err := configHelper.GetUserJSON(ctx, "extension.installed", &installed)
	if err != nil {
		return false, fmt.Errorf("failed to check installed azd extensions: %w", err)
	}
	if !found {
		return false, nil
	}

	_, ok := installed[agentInspectorExtensionID]
	return ok, nil
}

func inspectorLaunchWarning(err error) string {
	msg := err.Error()
	if st, ok := status.FromError(err); ok {
		msg = st.Message()
	}

	if isInspectorExtensionMissingMessage(msg) {
		return missingInspectorExtensionWarning()
	}

	return fmt.Sprintf("Warning: Agent Inspector was not launched: %v", err)
}

func missingInspectorExtensionWarning() string {
	return fmt.Sprintf(
		"Warning: Agent Inspector was not launched because the %s extension is not installed.\n"+
			"Install it with: azd extension install %s",
		agentInspectorExtensionID,
		agentInspectorExtensionID,
	)
}

func isInspectorExtensionMissingMessage(message string) bool {
	message = strings.ToLower(message)
	return (strings.Contains(message, "unknown command") && strings.Contains(message, "inspector")) ||
		(strings.Contains(message, "ai inspector launch") && strings.Contains(message, "unknown flag: --port"))
}

func isContextCancellation(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		status.Code(err) == codes.Canceled
}

func shouldWarnLoadAzdEnvironmentFailure(err error) bool {
	msg := err.Error()
	if st, ok := status.FromError(err); ok {
		msg = st.Message()
	}
	return !strings.Contains(strings.ToLower(msg), "default environment not found")
}

// resolveAgentDefinitionEnvVars loads agent.yaml from projectDir, extracts
// environment_variables, and resolves all value types:
//   - Hardcoded values are used as-is
//   - ${VAR} references are resolved using azdEnvVars via envsubst
//   - ${{connections.<name>.credentials.<key>}} are resolved via Foundry API
//
// Returns nil if no agent.yaml is found or it has no environment_variables.
// Errors during connection resolution are returned so the caller can decide
// whether to warn or fail.
func resolveAgentDefinitionEnvVars(
	ctx context.Context,
	projectDir string,
	azdEnvVars map[string]string,
	endpoint string,
) ([]string, error) {
	// Find agent.yaml in projectDir
	agentYamlPath := findAgentYaml(projectDir)
	if agentYamlPath == "" {
		return nil, nil
	}

	data, err := os.ReadFile(agentYamlPath) //nolint:gosec // G304: path from findAgentYaml which checks known filenames in projectDir
	if err != nil {
		return nil, fmt.Errorf("could not read agent definition %s: %w", agentYamlPath, err)
	}

	var agentDef agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &agentDef); err != nil {
		return nil, fmt.Errorf("could not parse agent definition %s: %w", agentYamlPath, err)
	}

	if agentDef.EnvironmentVariables == nil || len(*agentDef.EnvironmentVariables) == 0 {
		return nil, nil
	}

	// Separate connection refs from regular env vars
	envVars := *agentDef.EnvironmentVariables
	refs := extractConnectionRefs(envVars)
	connRefEnvNames := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		connRefEnvNames[ref.EnvName] = struct{}{}
	}

	// Build lookup function for envsubst
	lookup := func(varName string) string {
		if azdEnvVars == nil {
			return ""
		}
		return azdEnvVars[varName]
	}

	var result []string

	// Resolve non-connection env vars (hardcoded + ${VAR} references)
	for _, ev := range envVars {
		if _, isConn := connRefEnvNames[ev.Name]; isConn {
			continue
		}
		// ExpandEnv returns the original value on error, so a failed expansion is a no-op.
		resolved, _ := project.ExpandEnv(ev.Value, lookup)
		result = append(result, fmt.Sprintf("%s=%s", ev.Name, resolved))
	}

	// Resolve connection credential references via Foundry API
	if len(refs) > 0 && endpoint != "" {
		connEnv, err := resolveConnectionRefs(ctx, refs, endpoint)
		if err != nil {
			return result, fmt.Errorf("connection credential resolution failed: %w", err)
		}
		result = append(result, connEnv...)
	}

	return result, nil
}

// findAgentYaml locates the agent definition file in the given directory.
// After `azd ai agent init`, agent.yaml (and azure.yaml) are the sources of
// truth for the agent configuration. We intentionally do not look at
// agent.manifest.yaml here — that file is an import artifact used only during
// init and is not referenced at runtime.
func findAgentYaml(dir string) string {
	candidates := []string{"agent.yaml", "agent.yml"}
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// appendPortEnvVars appends PORT and, for .NET projects, ASPNETCORE_URLS to the
// environment slice so the agent listens on the correct port.
// ASP.NET Core ignores PORT — it uses ASPNETCORE_URLS to configure Kestrel.
func appendPortEnvVars(env []string, pt ProjectType, port int) []string {
	env = append(env, fmt.Sprintf("PORT=%d", port))
	if pt.Language == "dotnet" {
		env = append(env, fmt.Sprintf("ASPNETCORE_URLS=http://localhost:%d", port))
	}
	return env
}

// --- Dependency installation ---

func installDependencies(projectDir string) error {
	pt := detectProjectType(projectDir)

	switch pt.Language {
	case "python":
		return installPythonDeps(projectDir)
	case "node":
		return installNodeDeps(projectDir)
	case "dotnet":
		return nil
	}
	return nil
}

func installPythonDeps(projectDir string) error {
	if _, err := exec.LookPath("uv"); err != nil {
		fmt.Println("Warning: uv is not installed. Install it from https://docs.astral.sh/uv/")
		fmt.Println("Falling back to pip...")
		return installPythonDepsPip(projectDir)
	}

	venvDir := filepath.Join(projectDir, ".venv")
	if _, err := os.Stat(venvDir); os.IsNotExist(err) {
		fmt.Println("Setting up Python environment...")
		cmd := exec.Command("uv", "venv", venvDir, "--python", ">=3.12") //nolint:gosec // G204: venvDir is derived from the project directory path
		cmd.Dir = projectDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to create venv: %w", err)
		}
	}

	pythonPath := venvPython(venvDir)

	if fileExists(filepath.Join(projectDir, "pyproject.toml")) {
		fmt.Println("Installing dependencies (pyproject.toml)...")
		cmd := exec.Command("uv", "pip", "install", "-e", ".", "--python", pythonPath, "--prerelease", "allow", "--quiet") //nolint:gosec // G204: pythonPath is derived from the project venv directory
		cmd.Dir = projectDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("uv pip install failed: %w", err)
		}
		fmt.Println("  ✓ Dependencies installed (pyproject.toml)")
	}

	if fileExists(filepath.Join(projectDir, "requirements.txt")) {
		fmt.Println("Installing dependencies (requirements.txt)...")
		cmd := exec.Command("uv", "pip", "install", "-r", "requirements.txt", "--python", pythonPath, "--prerelease", "allow", "--quiet") //nolint:gosec // G204: pythonPath is derived from the project venv directory
		cmd.Dir = projectDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("uv pip install failed: %w", err)
		}
		fmt.Println("  ✓ Dependencies installed (requirements.txt)")
	}

	return nil
}

func installPythonDepsPip(projectDir string) error {
	if fileExists(filepath.Join(projectDir, "requirements.txt")) {
		fmt.Println("Installing dependencies (requirements.txt)...")
		cmd := exec.Command("pip", "install", "-r", "requirements.txt", "-q")
		cmd.Dir = projectDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pip install failed: %w", err)
		}
		fmt.Println("  ✓ Dependencies installed (requirements.txt)")
	}
	return nil
}

func installNodeDeps(projectDir string) error {
	if fileExists(filepath.Join(projectDir, "package.json")) {
		fmt.Println("Installing dependencies (package.json)...")
		cmd := exec.Command("npm", "install", "--quiet")
		cmd.Dir = projectDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("npm install failed: %w", err)
		}
		fmt.Println("  ✓ Dependencies installed (package.json)")
	}
	return nil
}

// --- Command parsing utilities ---

func parseCommand(cmd string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if inQuote {
			if c == quoteChar {
				inQuote = false
			} else {
				current.WriteByte(c)
			}
		} else if c == '"' || c == '\'' {
			inQuote = true
			quoteChar = c
		} else if c == ' ' {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func resolveVenvCommand(projectDir string, cmdParts []string) []string {
	if len(cmdParts) == 0 {
		return cmdParts
	}

	venvDir := filepath.Join(projectDir, ".venv")
	if _, err := os.Stat(venvDir); os.IsNotExist(err) {
		return cmdParts
	}

	pythonPath := venvPython(venvDir)

	if cmdParts[0] == "python" || cmdParts[0] == "python3" {
		cmdParts[0] = pythonPath
	} else {
		binDir := venvBinDir(venvDir)
		binPath := filepath.Join(binDir, cmdParts[0])
		if fileExists(binPath) {
			cmdParts[0] = binPath
		}
	}

	return cmdParts
}

func venvPython(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", "python.exe")
	}
	return filepath.Join(venvDir, "bin", "python")
}

func venvBinDir(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts")
	}
	return filepath.Join(venvDir, "bin")
}

// appendFoundryEnvVars translates azd environment keys to FOUNDRY_* env vars that hosted
// agent containers receive automatically from the platform. This ensures the agent code
// works identically whether running locally (via azd ai agent run) or in a hosted container.
//
// The mapping is:
//
//	AZURE_AI_PROJECT_ID                → FOUNDRY_PROJECT_ARM_ID
//	AGENT_{SVC}_NAME                   → FOUNDRY_AGENT_NAME
//	AGENT_{SVC}_VERSION                → FOUNDRY_AGENT_VERSION
//	APPLICATIONINSIGHTS_CONNECTION_STRING (unchanged — already matches platform name)
func appendFoundryEnvVars(env []string, azdEnv map[string]string, serviceName string) []string {
	// Static mappings from azd env key names to FOUNDRY_* env var names
	staticMappings := []struct {
		azdKey     string
		foundryKey string
	}{
		{"AZURE_AI_PROJECT_ID", "FOUNDRY_PROJECT_ARM_ID"},
	}

	for _, m := range staticMappings {
		if v := azdEnv[m.azdKey]; v != "" {
			if _, exists := azdEnv[m.foundryKey]; !exists && !envSliceHasKey(env, m.foundryKey) {
				env = append(env, fmt.Sprintf("%s=%s", m.foundryKey, v))
			}
		}
	}

	// Service-specific mappings (AGENT_{SVC}_NAME → FOUNDRY_AGENT_NAME, etc.)
	if serviceName != "" {
		serviceKey := toServiceKey(serviceName)
		agentMappings := []struct {
			azdKeyFmt  string
			foundryKey string
		}{
			{"AGENT_%s_NAME", "FOUNDRY_AGENT_NAME"},
			{"AGENT_%s_VERSION", "FOUNDRY_AGENT_VERSION"},
		}

		for _, m := range agentMappings {
			azdKey := fmt.Sprintf(m.azdKeyFmt, serviceKey)
			if v := azdEnv[azdKey]; v != "" {
				if _, exists := azdEnv[m.foundryKey]; !exists && !envSliceHasKey(env, m.foundryKey) {
					env = append(env, fmt.Sprintf("%s=%s", m.foundryKey, v))
				}
			}
		}
	}

	return env
}

// envSliceHasKey reports whether the env slice already contains an entry for the given key.
func envSliceHasKey(env []string, key string) bool {
	prefix := key + "="
	return slices.ContainsFunc(env, func(entry string) bool {
		return strings.HasPrefix(entry, prefix)
	})
}

// loadAzdEnvironment reads all key-value pairs from the current azd environment.
func loadAzdEnvironment(ctx context.Context, azdClient *azdext.AzdClient) (map[string]string, error) {
	envResponse, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, err
	}

	resp, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: envResponse.Environment.Name,
	})
	if err != nil {
		return nil, err
	}

	result := make(map[string]string, len(resp.KeyValues))
	for _, kv := range resp.KeyValues {
		result[kv.Key] = kv.Value
	}
	return result, nil
}

// emitNextAfterBind blocks until the agent process binds the local
// port (or the budget elapses, or ctx is cancelled) and then prints
// the protocol-appropriate `Next:` block. The state assembler is
// configured with both a live HTTP probe and the on-disk cache: the
// live spec wins when reachable, and the cache is the fallback when
// the agent doesn't expose /invocations/docs/openapi.json or fails
// its probe.
//
// Returns silently on every failure path (port never bound, ctx
// cancelled mid-wait, state assembly error, non-terminal stdout). The
// user already sees the agent's own stderr in those cases; surfacing
// additional diagnostics here would clutter an otherwise-busy terminal.
func emitNextAfterBind(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
	port int,
) {
	// Honor the nextstep call-site TTY-gating contract: when stdout
	// is redirected (e.g., `azd ai agent run > log`), the human-only
	// "Agent ready"/Next: block must not contaminate the capture.
	if !stdoutIsTerminal() {
		return
	}
	if !waitForPortReady(ctx, port, portReadyBudget) {
		return
	}
	liveFetch := func(probeCtx context.Context) ([]byte, error) {
		probeCtx, cancel := context.WithTimeout(probeCtx, liveOpenAPITimeout)
		defer cancel()
		return fetchLiveOpenAPI(probeCtx, port)
	}
	state, _ := nextstep.AssembleState(ctx, azdClient,
		nextstep.WithOpenAPIProbe(serviceName, "local"),
		nextstep.WithLiveOpenAPIProbe(liveFetch))
	// Re-check ctx after AssembleState: if Ctrl+C arrived mid-call,
	// the user already saw "Stopping agent..."/"Agent stopped." and
	// printing "Agent ready" now would be factually wrong.
	if ctx.Err() != nil {
		return
	}
	fmt.Println("\nAgent ready. In another terminal, try:")
	_ = printNextIfTerminal(os.Stdout, nextstep.ResolveAfterRun(state, serviceName, readmeExistsForProject(ctx, azdClient)))
}

// portReadyBudget is the wall-clock ceiling for waitForPortReady;
// most agent runtimes (uvicorn, dotnet, node) bind within a second
// of start so 5 s is generous without making a failed boot drag
// the user's attention.
const portReadyBudget = 5 * time.Second

// portReadyPollInterval is how often waitForPortReady probes the
// loopback address; 100 ms is short enough to feel snappy while
// keeping the wake-up count low on slow machines.
const portReadyPollInterval = 100 * time.Millisecond

// portReadyDialTimeout caps each individual dial; this stays well
// below portReadyPollInterval so a slow refusal doesn't drag the
// poll cadence beyond the configured rhythm.
const portReadyDialTimeout = 50 * time.Millisecond

// liveOpenAPITimeout caps the live /invocations/docs/openapi.json
// fetch issued by emitNextAfterBind. The design budget is 3 s — long
// enough for a freshly-bound server to honor the GET, short enough
// that a silent agent (no openapi route) doesn't visibly delay the
// `Next:` block.
const liveOpenAPITimeout = 3 * time.Second

// waitForPortReady polls localhost:port at portReadyPollInterval
// until a TCP dial succeeds or the budget elapses. Returns true on
// success. Respects ctx.Done so a Ctrl+C during boot doesn't block
// the wait — the goroutine exits cleanly.
func waitForPortReady(ctx context.Context, port int, budget time.Duration) bool {
	deadline := time.Now().Add(budget)
	addr := fmt.Sprintf("localhost:%d", port)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		conn, err := net.DialTimeout("tcp", addr, portReadyDialTimeout)
		if err == nil {
			_ = conn.Close()
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(portReadyPollInterval):
		}
	}
	return false
}

// fetchLiveOpenAPI issues an HTTP GET against
// /invocations/docs/openapi.json on the local agent and returns the
// response body. The route matches the cache-side fetcher in
// helpers.go (fetchOpenAPISpec) and the user-facing curl tip surfaced
// by nextstep/resolver.go. The caller is responsible for the
// surrounding timeout (we honor ctx). Non-200 responses are reported
// as errors so the state assembler falls back to the on-disk cache
// rather than feeding a stale or 404-shaped body into
// ExtractInvokeExample.
func fetchLiveOpenAPI(ctx context.Context, port int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://localhost:%d/invocations/docs/openapi.json", port), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openapi.json: %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}
