// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	copilot "github.com/github/copilot-sdk/go"
)

// CopilotClientManager manages the lifecycle of a Copilot SDK client.
// It wraps copilot.Client with azd-specific configuration and error handling.
type CopilotClientManager struct {
	client  *copilot.Client
	options *CopilotClientOptions
	cliPath string
}

// CopilotClientOptions configures the CopilotClientManager.
type CopilotClientOptions struct {
	// LogLevel controls SDK logging verbosity (e.g., "info", "debug", "error").
	LogLevel string
	// CLIPath overrides the path to the Copilot CLI binary.
	// If empty, auto-discovered from @github/copilot-sdk npm package or COPILOT_CLI_PATH env.
	CLIPath string
}

// NewCopilotClientManager creates a new CopilotClientManager with the given options.
// If options is nil, defaults are used.
func NewCopilotClientManager(options *CopilotClientOptions) *CopilotClientManager {
	if options == nil {
		options = &CopilotClientOptions{}
	}

	clientOpts := &copilot.ClientOptions{}
	if options.LogLevel != "" {
		clientOpts.LogLevel = options.LogLevel
	} else {
		clientOpts.LogLevel = "debug"
	}

	// Resolve CLI path: explicit option > env var > auto-discover from npm
	cliPath := options.CLIPath
	if cliPath == "" {
		cliPath = discoverCopilotCLIPath()
	}
	if cliPath != "" {
		clientOpts.CLIPath = cliPath
		log.Printf("[copilot-client] Using CLI binary: %s", cliPath)
	}

	return &CopilotClientManager{
		client:  copilot.NewClient(clientOpts),
		options: options,
		cliPath: cliPath,
	}
}

// Start initializes the Copilot SDK client and establishes a connection
// to the copilot-agent-runtime process.
func (m *CopilotClientManager) Start(ctx context.Context) error {
	log.Printf("[copilot-client] Starting client (logLevel=%q)...", m.options.LogLevel)
	log.Printf("[copilot-client] SDK will spawn copilot CLI process via stdio transport")
	if err := m.client.Start(ctx); err != nil {
		log.Printf("[copilot-client] Start failed: %v", err)
		log.Printf("[copilot-client] Ensure 'copilot' CLI is in PATH and supports SDK protocol")
		return fmt.Errorf(
			"failed to start Copilot agent runtime: %w",
			err,
		)
	}
	log.Printf("[copilot-client] Started successfully (state=%s)", m.client.State())
	return nil
}

// Stop gracefully shuts down the Copilot SDK client and terminates the agent runtime process.
func (m *CopilotClientManager) Stop() error {
	if m.client == nil {
		return nil
	}
	return m.client.Stop()
}

// Client returns the underlying copilot.Client for session creation.
func (m *CopilotClientManager) Client() *copilot.Client {
	return m.client
}

// GetAuthStatus checks whether the user is authenticated with GitHub Copilot.
func (m *CopilotClientManager) GetAuthStatus(ctx context.Context) (*copilot.GetAuthStatusResponse, error) {
	status, err := m.client.GetAuthStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check Copilot auth status: %w", err)
	}
	return status, nil
}

// ListModels returns the list of available models from the Copilot service.
func (m *CopilotClientManager) ListModels(ctx context.Context) ([]copilot.ModelInfo, error) {
	models, err := m.client.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Copilot models: %w", err)
	}
	return models, nil
}

// State returns the current connection state of the client.
func (m *CopilotClientManager) State() copilot.ConnectionState {
	return m.client.State()
}

// CLIPath returns the resolved path to the Copilot CLI binary.
func (m *CopilotClientManager) CLIPath() string {
	return m.cliPath
}

// discoverCopilotCLIPath finds the native Copilot CLI binary that supports
// the --headless --stdio flags required by the SDK.
//
// Resolution order:
//  1. COPILOT_CLI_PATH environment variable
//  2. Native binary bundled in @github/copilot-sdk npm package
//  3. Empty string (SDK will fall back to "copilot" in PATH)
func discoverCopilotCLIPath() string {
	if p := os.Getenv("COPILOT_CLI_PATH"); p != "" {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	// Map Go arch to npm platform naming
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "ia32"
	}

	var platformPkg, binaryName string
	switch runtime.GOOS {
	case "windows":
		platformPkg = fmt.Sprintf("copilot-win32-%s", arch)
		binaryName = "copilot.exe"
	case "darwin":
		platformPkg = fmt.Sprintf("copilot-darwin-%s", arch)
		binaryName = "copilot"
	case "linux":
		platformPkg = fmt.Sprintf("copilot-linux-%s", arch)
		binaryName = "copilot"
	default:
		return ""
	}

	// Search common npm global node_modules locations
	candidates := []string{
		filepath.Join(home, "AppData", "Roaming", "npm", "node_modules"),
		filepath.Join(home, ".npm-global", "lib", "node_modules"),
		"/usr/local/lib/node_modules",
		"/usr/lib/node_modules",
	}

	for _, c := range candidates {
		p := filepath.Join(c, "@github", "copilot-sdk", "node_modules", "@github", platformPkg, binaryName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return ""
}
