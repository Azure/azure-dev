// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

// cliVersion is the Copilot CLI version that matches the SDK version in go.mod.
// SDK v0.1.32 → CLI v1.0.2 (determined by the SDK's package-lock.json).
const cliVersion = "1.0.2"

// CopilotCLI manages the Copilot CLI binary lifecycle — download, cache, and version management.
// Follows the same pattern as pkg/tools/bicep for on-demand tool installation.
type CopilotCLI struct {
	path        string
	runner      azdexec.CommandRunner
	console     input.Console
	transporter policy.Transporter

	installOnce sync.Once
	installErr  error
}

var _ tools.ExternalTool = (*CopilotCLI)(nil)

// Name returns the display name of the tool.
func (c *CopilotCLI) Name() string {
	return "GitHub Copilot CLI"
}

// InstallUrl returns the documentation URL for manual installation.
func (c *CopilotCLI) InstallUrl() string {
	return "https://github.com/features/copilot/cli/"
}

// CheckInstalled verifies the Copilot CLI is available, downloading it if needed.
func (c *CopilotCLI) CheckInstalled(ctx context.Context) error {
	_, err := c.Path(ctx)
	return err
}

// ListPlugins returns a map of installed plugin names.
func (c *CopilotCLI) ListPlugins(ctx context.Context) (map[string]bool, error) {
	result, err := c.runCommand(ctx, "plugin", "list")
	if err != nil {
		return nil, fmt.Errorf("listing plugins: %w", err)
	}

	log.Printf("[copilot-cli] Plugin list output: %s", strings.TrimSpace(result.Stdout))

	installed := make(map[string]bool)
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "•") || strings.HasPrefix(line, "\u2022") {
			name := strings.TrimPrefix(line, "•")
			name = strings.TrimPrefix(name, "\u2022")
			name = strings.TrimSpace(name)
			if idx := strings.Index(name, " "); idx > 0 {
				name = name[:idx]
			}
			if name != "" {
				installed[name] = true
			}
		}
	}
	return installed, nil
}

// InstallPlugin installs a plugin by source reference.
func (c *CopilotCLI) InstallPlugin(ctx context.Context, source string) error {
	result, err := c.runCommand(ctx, "plugin", "install", source)
	if err != nil {
		return fmt.Errorf("installing plugin: %w", err)
	}

	log.Printf("[copilot-cli] %s", strings.TrimSpace(result.Stdout))
	return nil
}

// Login runs the copilot login command (OAuth device flow).
func (c *CopilotCLI) Login(ctx context.Context) error {
	runArgs := azdexec.NewRunArgs("copilot", "login").
		WithInteractive(true)
	_, err := c.runner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("copilot login failed: %w", err)
	}
	return nil
}

// runCommand executes a copilot CLI command using the command runner.
// Uses "copilot" from PATH for plugin management commands (the npm-distributed
// native binary doesn't support CLI subcommands like "plugin list").
func (c *CopilotCLI) runCommand(ctx context.Context, args ...string) (azdexec.RunResult, error) {
	runArgs := azdexec.NewRunArgs("copilot", args...)
	return c.runner.Run(ctx, runArgs)
}

// NewCopilotCLI creates a new CopilotCLI manager.
func NewCopilotCLI(console input.Console, runner azdexec.CommandRunner, transporter policy.Transporter) *CopilotCLI {
	return &CopilotCLI{
		console:     console,
		runner:      runner,
		transporter: transporter,
	}
}

// Path returns the path to the Copilot CLI binary, downloading it if necessary.
// Safe to call multiple times; installation only happens once.
func (c *CopilotCLI) Path(ctx context.Context) (string, error) {
	c.installOnce.Do(func() {
		c.installErr = c.ensureInstalled(ctx)
	})
	return c.path, c.installErr
}

func (c *CopilotCLI) ensureInstalled(ctx context.Context) error {
	// Check for explicit azd override first
	if override := os.Getenv("AZD_COPILOT_CLI_PATH"); override != "" {
		//nolint:gosec // G706: env var in debug log
		log.Printf("[copilot-cli] Using override: %s", override)
		c.path = override
		return nil
	}

	cliPath, err := copilotCLIPath()
	if err != nil {
		return fmt.Errorf("resolving copilot CLI path: %w", err)
	}

	if _, err := os.Stat(cliPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking copilot CLI: %w", err)
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(cliPath), osutil.PermissionDirectory); err != nil {
			return fmt.Errorf("creating copilot CLI directory: %w", err)
		}

		c.console.ShowSpinner(ctx, "Downloading GitHub Copilot CLI", input.Step)
		err := downloadCopilotCLI(ctx, c.transporter, cliVersion, cliPath)
		if err != nil {
			c.console.StopSpinner(ctx, "Downloading GitHub Copilot CLI", input.StepFailed)
			return fmt.Errorf("downloading copilot CLI: %w", err)
		}
		c.console.StopSpinner(ctx, "Downloading GitHub Copilot CLI", input.StepDone)
		c.console.Message(ctx, "")
	}

	c.path = cliPath
	log.Printf("[copilot-cli] Using: %s (version %s)", cliPath, cliVersion)
	return nil
}

// copilotCLIPath returns the cache path for the Copilot CLI binary.
func copilotCLIPath() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}

	binaryName := fmt.Sprintf("copilot-cli-%s", cliVersion)
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	return filepath.Join(configDir, "bin", binaryName), nil
}

// downloadCopilotCLI downloads the Copilot CLI binary from the npm registry.
// The npm package is a tgz containing the binary at package/copilot[.exe].
func downloadCopilotCLI(ctx context.Context, transporter policy.Transporter, version string, destPath string) error {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x64"
	case "arm64":
		// arm64 stays as-is
	default:
		return fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}

	var platform string
	switch runtime.GOOS {
	case "windows":
		platform = "win32"
	case "darwin":
		platform = "darwin"
	case "linux":
		platform = "linux"
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	pkgName := fmt.Sprintf("copilot-%s-%s", platform, arch)
	downloadURL := fmt.Sprintf("https://registry.npmjs.org/@github/%s/-/%s-%s.tgz", pkgName, pkgName, version)

	log.Printf("[copilot-cli] Downloading %s -> %s", downloadURL, destPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return err
	}

	resp, err := transporter.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d from %s", resp.StatusCode, downloadURL)
	}

	// Extract the binary from the tgz
	return extractBinaryFromTgz(resp.Body, destPath)
}

// extractBinaryFromTgz extracts the copilot binary from an npm package tgz.
// The binary is at package/copilot[.exe] inside the tar.
func extractBinaryFromTgz(reader io.Reader, destPath string) error {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return fmt.Errorf("decompressing tgz: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	binaryName := "copilot"
	if runtime.GOOS == "windows" {
		binaryName = "copilot.exe"
	}

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}

		// npm packages have files at package/<name>
		name := filepath.Base(header.Name)
		if !strings.EqualFold(name, binaryName) {
			continue
		}

		// Write to temp file, then atomic rename
		tmpFile, err := os.CreateTemp(filepath.Dir(destPath), "copilot-cli-*.tmp")
		if err != nil {
			return fmt.Errorf("creating temp file: %w", err)
		}
		defer func() {
			tmpFile.Close()
			os.Remove(tmpFile.Name()) //nolint:gosec // G703: temp file cleanup
		}()

		// Limit extraction to 200MB to prevent decompression bombs
		const maxBinarySize = 200 * 1024 * 1024
		limited := io.LimitReader(tr, maxBinarySize)
		if _, err := io.Copy(tmpFile, limited); err != nil {
			return fmt.Errorf("extracting binary: %w", err)
		}

		if err := tmpFile.Chmod(osutil.PermissionExecutableFile); err != nil {
			return fmt.Errorf("setting permissions: %w", err)
		}

		if err := tmpFile.Close(); err != nil {
			return err
		}

		if err := osutil.Rename(context.Background(), tmpFile.Name(), destPath); err != nil {
			return fmt.Errorf("installing binary: %w", err)
		}

		log.Printf("[copilot-cli] Extracted %s (%d bytes)", binaryName, header.Size)
		return nil
	}

	return fmt.Errorf("binary %q not found in tgz", binaryName)
}
