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
	"maps"
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
	noClient     bool
	channel      string
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

By default, this also opens a local client after the agent starts listening:
Agent Inspector for responses/invocations agents, or the Microsoft 365 Agents
Playground for activity agents. Use --no-client to skip this.`,
		Example: `  # Start the agent in the current directory
  azd ai agent run

  # Start a specific agent by name
  azd ai agent run my-agent

  # Start on a custom port
  azd ai agent run --port 9090

  # Start without opening a local client
  azd ai agent run --no-client

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
	cmd.Flags().BoolVar(&flags.noInspector, "no-inspector", false, "Do not open the local client (Agent Inspector or Playground)")
	cmd.Flags().BoolVar(&flags.noClient, "no-client", false,
		"Do not open the local client (Agent Inspector or Playground)")
	// --no-inspector predates the Playground; --no-client is the canonical,
	// protocol-neutral name. Keep --no-inspector working for back-compat but
	// hide it from help and nudge users to --no-client.
	_ = cmd.Flags().MarkDeprecated("no-inspector", "use --no-client instead")
	cmd.Flags().StringVar(&flags.channel, "channel", defaultPlaygroundChannel,
		"Channel for the Microsoft 365 Agents Playground (activity-protocol agents only)")

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

	// Detect whether the target service is an activity agent.
	// This is the single gate that keeps all activity-specific local behavior off
	// the path of non-activity (responses/invocations) agents — they are entirely
	// unaffected. Detection is self-contained (reads the agent definition), so
	// this command has no dependency on the deploy-side activity work.
	activityProfile := resolveActivityRunProfile(runCtx.Definition)

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

	env := appendPortEnvVars(os.Environ(), pt, flags.port)

	// Load azd values as template inputs and legacy fallback values.
	var azdEnvVars map[string]string
	if loaded, err := loadAzdEnvironment(ctx, azdClient); err == nil {
		azdEnvVars = loaded
	} else if shouldWarnLoadAzdEnvironmentFailure(err) {
		fmt.Fprintf(os.Stderr, "Warning: failed to load azd environment values: %s\n", err)
	}

	endpoint, _ := resolveAgentEndpoint(ctx, "", "")
	endpoint = localProjectEndpoint(
		env,
		runCtx.ServiceEnvironment,
		endpoint,
	)
	serviceEnvironment := resolveLocalServiceEnvironment(
		runCtx.ServiceEnvironment,
		endpoint,
	)
	defEnv, defErr := resolveAgentDefinitionEnvVars(
		ctx,
		runCtx.Definition,
		serviceEnvironment,
		azdEnvVars,
		endpoint,
	)
	if defErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", defErr)
	}
	env = mergeAgentRunEnvironment(
		env,
		azdEnvVars,
		serviceEnvironment,
		defEnv,
		runCtx.ServiceName,
	)

	// Activity agents bind IPv4 and are reached at 127.0.0.1 everywhere else
	// (the port-readiness check and the Playground URL), because `localhost`
	// can resolve to IPv6 ::1 first and fail the connection. Keep the display
	// URL consistent so a user copying it hits the same address that works.
	displayHost := "localhost"
	if activityProfile.IsActivity {
		displayHost = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s:%d", displayHost, flags.port)

	// Activity agents only round-trip locally in the anonymous
	// "digital-worker" auth model. The default "simple" model needs a managed
	// identity that doesn't exist off-box, so every message 500s locally.
	// Force the toggle on for the local process only — deploy is unaffected
	// because this env var is never set outside `run`. Appended last so it wins
	// over any duplicate (Go exec uses the last value for a duplicate key).
	if activityProfile.IsActivity {
		env = append(env, fmt.Sprintf("%s=1", agentDigitalWorkerEnvVar))
		fmt.Printf(
			"Activity agent detected: starting in digital-worker (anonymous) mode for local Playground testing.\n")
	}

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

	// Auto-launch the local client once the agent binds its port. Activity
	// agents use the Microsoft 365 Agents Playground (the only local client that
	// speaks the Activity protocol); everything else uses Agent Inspector. Both
	// are suppressed by --no-inspector or its neutral alias --no-client.
	suppressClient := flags.noInspector || flags.noClient
	if activityProfile.IsActivity {
		handlePlaygroundAutoLaunch(ctx, flags.port, flags.channel, suppressClient, os.Stderr)
	} else {
		inspectorInstalled := false
		var inspectorInstallErr error
		if !suppressClient {
			inspectorInstalled, inspectorInstallErr = isInspectorExtensionInstalled(ctx, azdClient)
		}
		handleInspectorAutoLaunch(
			ctx,
			azdClient.Workflow(),
			flags.port,
			suppressClient,
			inspectorInstalled,
			inspectorInstallErr,
			os.Stderr,
		)
	}

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

// resolveAgentDefinitionEnvVars takes a resolved agent definition, extracts its
// environment_variables, and resolves all value types:
//   - Hardcoded values are used as-is
//   - ${VAR} references are resolved using azdEnvVars via envsubst
//   - ${{connections.<name>.credentials.<key>}} are resolved via Foundry API
//
// Returns nil when the definition is nil or has no environment_variables.
// Errors during connection resolution are returned so the caller can decide
// whether to warn or fail.
func resolveAgentDefinitionEnvVars(
	ctx context.Context,
	agentDef *agent_yaml.ContainerAgent,
	serviceEnvironment map[string]string,
	azdEnvVars map[string]string,
	endpoint string,
) ([]string, error) {
	if agentDef == nil || agentDef.EnvironmentVariables == nil || len(*agentDef.EnvironmentVariables) == 0 {
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
		resolved, err := project.ResolveAgentEnvironmentVariable(
			ev.Name,
			ev.Value,
			serviceEnvironment,
			lookup,
		)
		if err != nil {
			resolved = ev.Value
		}
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

func resolveLocalServiceEnvironment(
	environment map[string]string,
	endpoint string,
) map[string]string {
	resolved := maps.Clone(environment)
	if endpoint == "" {
		return resolved
	}
	for key, value := range resolved {
		resolved[key] = strings.ReplaceAll(
			value,
			"${{project.endpoint}}",
			endpoint,
		)
	}
	return resolved
}

func localProjectEndpoint(
	baseEnvironment []string,
	serviceEnvironment map[string]string,
	fallback string,
) string {
	if value, found := envSliceValue(
		baseEnvironment,
		"FOUNDRY_PROJECT_ENDPOINT",
	); found {
		return value
	}
	if value, found := serviceEnvironment["FOUNDRY_PROJECT_ENDPOINT"]; found {
		return value
	}
	return fallback
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
		cmd := exec.Command("uv", "venv", venvDir, "--python", minPythonUvSpec()) //nolint:gosec // G204: venvDir is derived from the project directory path
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
	venvDir := filepath.Join(projectDir, ".venv")

	info, err := os.Stat(venvDir)
	switch {
	case err == nil && !info.IsDir():
		return fmt.Errorf(".venv exists but is not a directory")
	case err != nil && !os.IsNotExist(err):
		return fmt.Errorf("failed to check .venv: %w", err)
	case os.IsNotExist(err):
		fmt.Println("Setting up Python environment...")
		python, findErr := findSystemPython()
		if findErr != nil {
			return findErr
		}
		cmd := python.command("-m", "venv", venvDir)
		cmd.Dir = projectDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to create venv: %w", err)
		}
	}

	pipPath := venvPip(venvDir)

	// If the venv was created by uv (which omits pip by default), bootstrap pip.
	if !fileExists(pipPath) {
		pythonPath := venvPython(venvDir)
		fmt.Println("Bootstrapping pip in virtual environment...")
		//nolint:gosec // G204: pythonPath is derived from the project venv directory
		cmd := exec.Command(pythonPath, "-m", "ensurepip", "--upgrade")
		cmd.Dir = projectDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to bootstrap pip in venv: %w", err)
		}
	}

	if fileExists(filepath.Join(projectDir, "pyproject.toml")) {
		fmt.Println("Installing dependencies (pyproject.toml)...")
		//nolint:gosec // G204: pipPath is derived from the project venv directory
		cmd := exec.Command(pipPath, "install", "-e", ".", "-q")
		cmd.Dir = projectDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pip install failed: %w", err)
		}
		fmt.Println("  ✓ Dependencies installed (pyproject.toml)")
	}

	if fileExists(filepath.Join(projectDir, "requirements.txt")) {
		fmt.Println("Installing dependencies (requirements.txt)...")
		//nolint:gosec // G204: pipPath is derived from the project venv directory
		cmd := exec.Command(pipPath, "install", "-r", "requirements.txt", "-q")
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

func venvPip(venvDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvDir, "Scripts", "pip.exe")
	}
	return filepath.Join(venvDir, "bin", "pip")
}

// Minimum supported system Python runtime. This is the single source of truth
// for the required Python version across both the uv and pip installation
// paths (see minPythonUvSpec and the version checks below).
const (
	minPythonMajor = 3
	minPythonMinor = 13
)

// minPythonUvSpec returns the minimum runtime as a uv `--python` version
// specifier (e.g. ">=3.13"), derived from the constants so the uv path stays in
// sync with the pip-path version checks.
func minPythonUvSpec() string {
	return fmt.Sprintf(">=%d.%d", minPythonMajor, minPythonMinor)
}

// pythonInterpreter is a resolved interpreter invocation: the executable to run
// plus any leading launcher arguments needed to select a specific version (for
// example the Windows `py -3` launcher, which selects the newest installed
// Python 3 rather than whichever python.exe happens to appear first on PATH).
type pythonInterpreter struct {
	// path is the resolved path to the interpreter or launcher (from LookPath).
	path string
	// args are leading arguments applied before any command (e.g. ["-3"]).
	args []string
}

// command builds an *exec.Cmd that invokes the interpreter with the provided
// trailing arguments, preserving any launcher args (e.g. `py -3 -m venv ...`).
func (p pythonInterpreter) command(extra ...string) *exec.Cmd {
	args := append(slices.Clone(p.args), extra...)
	//nolint:gosec // G204: path is an exec.LookPath result; args are static/derived
	return exec.Command(p.path, args...)
}

// pythonCandidates returns the ordered interpreter candidates to probe, limited
// to those that resolve on PATH. On Windows the `py -3` launcher is preferred
// because it selects the newest installed Python 3, which is frequently newer
// than the first python.exe on PATH.
func pythonCandidates() []pythonInterpreter {
	type candidate struct {
		name string
		args []string
	}

	var candidates []candidate
	if runtime.GOOS == "windows" {
		candidates = []candidate{
			{"py", []string{"-3"}},
			{"python3", nil},
			{"python", nil},
			{"py", nil},
		}
	} else {
		candidates = []candidate{
			{"python3", nil},
			{"python", nil},
		}
	}

	var resolved []pythonInterpreter
	for _, c := range candidates {
		p, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		resolved = append(resolved, pythonInterpreter{path: p, args: c.args})
	}
	return resolved
}

// pythonVersion runs `--version` on the interpreter and returns the parsed major
// and minor version numbers along with the raw version string (e.g. "3.13.2").
func pythonVersion(interpreter pythonInterpreter) (major, minor int, raw string, err error) {
	out, err := interpreter.command("--version").Output()
	if err != nil {
		return 0, 0, "", fmt.Errorf("failed to check Python version: %w", err)
	}

	// Output is like "Python 3.13.2".
	version := strings.TrimSpace(string(out))
	parts := strings.SplitN(version, " ", 2)
	if len(parts) != 2 {
		return 0, 0, "", fmt.Errorf("unexpected python --version output: %s", version)
	}
	raw = parts[1]

	segments := strings.SplitN(raw, ".", 3)
	if len(segments) < 2 {
		return 0, 0, "", fmt.Errorf("unexpected python version format: %s", raw)
	}

	major, err = strconv.Atoi(segments[0])
	if err != nil {
		return 0, 0, "", fmt.Errorf("unexpected python major version: %s", segments[0])
	}
	minor, err = strconv.Atoi(segments[1])
	if err != nil {
		return 0, 0, "", fmt.Errorf("unexpected python minor version: %s", segments[1])
	}

	return major, minor, raw, nil
}

// pythonVersionOK reports whether the given version satisfies the minimum
// supported Python runtime.
func pythonVersionOK(major, minor int) bool {
	return major > minPythonMajor || (major == minPythonMajor && minor >= minPythonMinor)
}

// firstCompatiblePython returns the first candidate whose version satisfies the
// minimum supported runtime. Candidates whose version cannot be determined are
// skipped. If every resolvable candidate is too old, the error names the newest
// incompatible version found so the user knows what is on their machine.
func firstCompatiblePython(
	candidates []pythonInterpreter,
	versionFn func(pythonInterpreter) (int, int, string, error),
) (pythonInterpreter, error) {
	newestRaw := ""
	newestMajor, newestMinor := -1, -1
	for _, c := range candidates {
		major, minor, raw, err := versionFn(c)
		if err != nil {
			continue
		}
		if pythonVersionOK(major, minor) {
			return c, nil
		}
		if major > newestMajor || (major == newestMajor && minor > newestMinor) {
			newestMajor, newestMinor, newestRaw = major, minor, raw
		}
	}

	if newestRaw != "" {
		return pythonInterpreter{}, fmt.Errorf(
			"Python %d.%d+ is required (found %s). "+
				"Install Python %d.%d+ from https://www.python.org/downloads/",
			minPythonMajor, minPythonMinor, newestRaw, minPythonMajor, minPythonMinor)
	}

	return pythonInterpreter{}, fmt.Errorf(
		"no compatible Python found on PATH. "+
			"Install Python %d.%d+ from https://www.python.org/downloads/",
		minPythonMajor, minPythonMinor)
}

// findSystemPython locates a system Python interpreter that satisfies the
// minimum supported runtime (>= 3.13). It probes several candidates and checks
// each one's version rather than blindly using the first python on PATH,
// because on Windows the first python.exe on PATH is frequently older than the
// newest installed Python (which the `py -3` launcher can locate).
func findSystemPython() (pythonInterpreter, error) {
	return firstCompatiblePython(pythonCandidates(), pythonVersion)
}

// mergeAgentRunEnvironment builds the local agent environment.
//
// baseEnvironment contains process and command-owned values.
// azdEnvironment is the full active environment for legacy fallback.
// serviceEnvironment is core-expanded services.<name>.env.
// definitionEnvironment comes from legacy agent definitions.
func mergeAgentRunEnvironment(
	baseEnvironment []string,
	azdEnvironment map[string]string,
	serviceEnvironment map[string]string,
	definitionEnvironment []string,
	serviceName string,
) []string {
	environment := slices.Clone(baseEnvironment)

	// The full azd environment is a compatibility fallback only.
	if len(serviceEnvironment) == 0 {
		for key, value := range azdEnvironment {
			if !envSliceHasKey(baseEnvironment, key) {
				environment = append(
					environment,
					fmt.Sprintf("%s=%s", key, value),
				)
			}
		}
	} else {
		for key, value := range serviceEnvironment {
			if !envSliceHasKey(baseEnvironment, key) {
				environment = append(
					environment,
					fmt.Sprintf("%s=%s", key, value),
				)
			}
		}
	}

	environment = appendFoundryEnvVars(
		environment,
		azdEnvironment,
		serviceName,
	)

	for _, entry := range definitionEnvironment {
		key, _, _ := strings.Cut(entry, "=")
		_, serviceScoped := serviceEnvironment[key]
		if serviceScoped {
			continue
		}
		if !envSliceHasKey(environment, key) {
			environment = append(environment, entry)
		}
	}

	return environment
}

// appendFoundryEnvVars adds values injected by hosted agents.
//
// The mapping is:
//
//	FOUNDRY_PROJECT_ENDPOINT           -> unchanged
//	AZURE_AI_PROJECT_ID                -> FOUNDRY_PROJECT_ARM_ID
//	AGENT_{SVC}_NAME                   -> FOUNDRY_AGENT_NAME
//	AGENT_{SVC}_VERSION                -> FOUNDRY_AGENT_VERSION
//	APPLICATIONINSIGHTS_CONNECTION_STRING -> unchanged
func appendFoundryEnvVars(env []string, azdEnv map[string]string, serviceName string) []string {
	env = appendEnvValue(
		env,
		"FOUNDRY_PROJECT_ENDPOINT",
		azdEnv["FOUNDRY_PROJECT_ENDPOINT"],
	)

	projectArmID := azdEnv["FOUNDRY_PROJECT_ARM_ID"]
	if projectArmID == "" {
		projectArmID = azdEnv["AZURE_AI_PROJECT_ID"]
	}
	env = appendEnvValue(env, "FOUNDRY_PROJECT_ARM_ID", projectArmID)

	agentName := ""
	agentVersion := ""
	if serviceName != "" {
		serviceKey := toServiceKey(serviceName)
		agentName = azdEnv[fmt.Sprintf("AGENT_%s_NAME", serviceKey)]
		agentVersion = azdEnv[fmt.Sprintf("AGENT_%s_VERSION", serviceKey)]
	}
	if agentName == "" {
		agentName = azdEnv["FOUNDRY_AGENT_NAME"]
	}
	if agentVersion == "" {
		agentVersion = azdEnv["FOUNDRY_AGENT_VERSION"]
	}
	env = appendEnvValue(env, "FOUNDRY_AGENT_NAME", agentName)
	env = appendEnvValue(env, "FOUNDRY_AGENT_VERSION", agentVersion)

	env = appendEnvValue(
		env,
		"APPLICATIONINSIGHTS_CONNECTION_STRING",
		azdEnv["APPLICATIONINSIGHTS_CONNECTION_STRING"],
	)

	return env
}

func appendEnvValue(env []string, key string, value string) []string {
	if value == "" || envSliceHasKey(env, key) {
		return env
	}
	return append(env, fmt.Sprintf("%s=%s", key, value))
}

// envSliceHasKey reports whether env contains an entry for key.
func envSliceHasKey(env []string, key string) bool {
	_, found := envSliceValue(env, key)
	return found
}

func envSliceValue(env []string, key string) (string, bool) {
	for _, entry := range env {
		entryKey, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if runtime.GOOS == "windows" {
			if strings.EqualFold(entryKey, key) {
				return value, true
			}
			continue
		}
		if entryKey == key {
			return value, true
		}
	}
	return "", false
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

// portReadyBudget is the wall-clock ceiling for waitForPortReady, which
// spans process-start → the server accepting on the loopback port.
// (Dependency setup — venv creation + pip install — runs before the
// server process is spawned, so it is NOT part of this window.) The gap
// this budget covers is the interpreter booting and importing the agent
// stack before it binds: empirically ~3–5 s for a minimal sample and
// ~8 s once heavier frameworks (e.g. agent_framework) are imported, with
// more on cold caches or slow CI. A short budget gives up before the
// listener is up, so the "Agent ready" signal never prints and a user
// (or coding agent) following the quickstart fires `invoke --local`
// against a listener that isn't accepting yet. 90 s leaves generous
// headroom for slow imports while still bounding a genuinely stuck boot.
// The budget is effectively free: it only governs a background goroutine
// that prints a readiness hint, and waitForPortReady honors ctx.Done, so
// Ctrl+C during the wait returns immediately. See issue #8411.
const portReadyBudget = 90 * time.Second

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
