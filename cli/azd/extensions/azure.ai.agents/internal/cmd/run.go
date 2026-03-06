// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type runFlags struct {
	port         int
	name         string
	startCommand string
}

func newRunCommand() *cobra.Command {
	flags := &runFlags{}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run your agent locally for development.",
		Long: `Run your agent locally for development.

Detects the project type (Python, .NET, Node.js), installs dependencies,
and starts the agent server in the foreground. Press Ctrl+C to stop.

The startup command is read from the startupCommand property of the
agent service in azure.yaml. If not set, it is auto-detected from the
project type. Use --start-command to override both.

Use a separate terminal to invoke the running agent:
  azd ai agent invoke --message "Hello!"`,
		Example: `  # Start the agent in the current directory
  azd ai agent run

  # Start a specific agent by name
  azd ai agent run --name my-agent

  # Start on a custom port
  azd ai agent run --port 9090

  # Start with an explicit command
  azd ai agent run --start-command "python app.py"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			setupDebugLogging(cmd.Flags())
			return runRun(ctx, flags)
		},
	}

	cmd.Flags().IntVarP(&flags.port, "port", "p", DefaultPort, "Port to listen on")
	cmd.Flags().StringVarP(&flags.name, "name", "n", "", "Agent service name (from azure.yaml)")
	cmd.Flags().StringVarP(&flags.startCommand, "start-command", "c", "",
		"Explicit startup command (overrides azure.yaml and auto-detection)")

	return cmd
}

func runRun(ctx context.Context, flags *runFlags) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	// Resolve the service source directory and startup command from azure.yaml
	runCtx, err := resolveServiceRunContext(ctx, azdClient, flags.name)
	if err != nil {
		return err
	}
	projectDir := runCtx.ProjectDir

	// Resolve start command: --start-command flag > azure.yaml startupCommand > detect
	startCmd := flags.startCommand
	if startCmd == "" {
		startCmd = runCtx.StartupCommand
	}

	if startCmd == "" {
		pt := detectProjectType(projectDir)
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
	env = append(env, fmt.Sprintf("PORT=%d", flags.port))

	// Load azd environment variables (e.g., AZURE_AI_PROJECT_ENDPOINT)
	// so the agent can reach Azure services during local development
	if azdEnvVars, err := loadAzdEnvironment(ctx, azdClient); err == nil {
		for k, v := range azdEnvVars {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	url := fmt.Sprintf("http://localhost:%d", flags.port)
	fmt.Println()
	fmt.Println("After startup, in another terminal, try:")
	fmt.Printf("  azd ai agent invoke \"Hello!\"\n\n")
	fmt.Printf("Starting agent on %s (Ctrl+C to stop)\n\n", url)

	// Create command with stdout/stderr piped to terminal
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	proc := exec.CommandContext(ctx, cmdParts[0], cmdParts[1:]...)
	proc.Dir = projectDir
	proc.Env = env
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	proc.Stdin = os.Stdin

	if err := proc.Start(); err != nil {
		return fmt.Errorf("failed to start agent: %w", err)
	}

	// Handle Ctrl+C: forward signal to child, then wait for it to exit
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\nStopping agent...")
		cancel()
	}()

	err = proc.Wait()

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
		cmd := exec.Command("uv", "venv", venvDir, "--python", ">=3.12")
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
		cmd := exec.Command("uv", "pip", "install", "-e", ".", "--python", pythonPath, "--prerelease", "allow", "--quiet")
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
		cmd := exec.Command("uv", "pip", "install", "-r", "requirements.txt", "--python", pythonPath, "--prerelease", "allow", "--quiet")
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
