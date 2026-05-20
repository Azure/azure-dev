// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
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

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	agentInspectorExtensionID     = "azure.ai.inspector"
	agentInspectorReadyTimeout    = 30 * time.Second
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
listening. Use --no-inspector to skip this.

Use a separate terminal to invoke the running agent:
  azd ai agent invoke --local "Hello!"`,
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

	// Load azd environment variables (e.g., AZURE_AI_PROJECT_ENDPOINT)
	// so the agent can reach Azure services during local development.
	// Also translate azd env keys to FOUNDRY_* env vars so the agent code
	// works identically whether running locally or in a hosted container
	// (where the platform automatically injects FOUNDRY_* env vars).
	if azdEnvVars, err := loadAzdEnvironment(ctx, azdClient); err == nil {
		for k, v := range azdEnvVars {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		env = appendFoundryEnvVars(env, azdEnvVars, runCtx.ServiceName)
	}

	// Resolve ${{connections.<name>.credentials.<key>}} references from the
	// agent manifest's environment_variables section. These are fetched from
	// the Foundry data plane at runtime and injected into the agent process.
	// Uses the same endpoint resolution as other agent commands.
	if endpoint, err := resolveAgentEndpoint(ctx, "", ""); err == nil {
		if connEnv, err := resolveConnectionCredentials(ctx, projectDir, endpoint); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: connection credential resolution failed: %s\n", err)
		} else {
			env = append(env, connEnv...)
		}
	}

	url := fmt.Sprintf("http://localhost:%d", flags.port)
	fmt.Println()
	fmt.Println("After startup, in another terminal, try:")
	fmt.Printf("  azd ai agent invoke --local \"Hello!\"\n\n")
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

	if !flags.noInspector {
		inspectorInstalled, err := isInspectorExtensionInstalled(ctx, azdClient)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Agent Inspector was not launched: %v\n", err)
		} else if !inspectorInstalled {
			fmt.Fprintln(os.Stderr, missingInspectorExtensionWarning())
		} else {
			startInspectorAfterAgentReady(ctx, azdClient.Workflow(), flags.port)
		}
	}

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

func startInspectorAfterAgentReady(ctx context.Context, workflow azdext.WorkflowServiceClient, agentPort int) {
	go func() {
		waitCtx, cancel := context.WithTimeout(ctx, agentInspectorReadyTimeout)
		defer cancel()

		if err := waitForLocalPort(waitCtx, agentPort, agentInspectorReadyPollPeriod); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(
					os.Stderr,
					"Warning: Agent Inspector was not launched because localhost:%d was not ready: %v\n",
					agentPort,
					err,
				)
			}
			return
		}

		if err := launchInspector(ctx, workflow, agentPort); err != nil && !isContextCancellation(err) {
			fmt.Fprintln(os.Stderr, inspectorLaunchWarning(err))
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
//	AZURE_AI_PROJECT_ENDPOINT          → FOUNDRY_PROJECT_ENDPOINT
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
		{"AZURE_AI_PROJECT_ENDPOINT", "FOUNDRY_PROJECT_ENDPOINT"},
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
