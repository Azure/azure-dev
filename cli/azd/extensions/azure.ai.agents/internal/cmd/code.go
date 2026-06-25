// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newCodeCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "code",
		Short: "Manage agent source code. (Preview)",
		Long:  `Commands for managing the source code of code-based hosted agents.`,
	}

	cmd.AddCommand(newCodeDownloadCommand(extCtx))

	return cmd
}

type codeDownloadFlags struct {
	name    string
	version string
	dest    string
	zip     bool
}

// CodeDownloadAction handles downloading agent source code.
type CodeDownloadAction struct {
	*AgentContext
	flags *codeDownloadFlags
}

func newCodeDownloadCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &codeDownloadFlags{}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "download [service]",
		Short: "Download agent source code from Foundry. (Preview)",
		Long: `Download the source code of a code-based hosted agent.

Downloads the deployed source code as a zip archive and extracts it to
the output directory. Use --zip to save the raw zip file instead.

The [service] argument identifies the azd service whose agent name is resolved
from the azd environment. When omitted, the default agent service is used.`,
		Example: `  # Download latest version (extracts to ./my-agent/)
  azd ai agent code download my-agent

  # Download a specific version
  azd ai agent code download my-agent --version 3

  # Save as zip file without extracting
  azd ai agent code download my-agent --zip

  # Download to a custom directory
  azd ai agent code download my-agent --dest ./backup`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				flags.name = args[0]
			}

			ctx := azdext.WithAccessToken(cmd.Context())

			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			info, err := resolveAgentServiceFromProject(ctx, azdClient, flags.name, extCtx.NoPrompt)
			if err != nil {
				return err
			}

			agentName := info.AgentName
			if agentName == "" {
				return fmt.Errorf(
					"agent name could not be resolved from azd environment for service '%s'\n\n"+
						"Run 'azd deploy' first to deploy the agent, or provide the agent name as a positional argument",
					info.ServiceName,
				)
			}

			agentContext, err := newAgentContext(ctx, "", "", agentName, info.Version)
			if err != nil {
				return err
			}

			action := &CodeDownloadAction{
				AgentContext: agentContext,
				flags:        flags,
			}

			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVarP(&flags.version, "version", "v", "", "Agent version to download (default: latest)")
	cmd.Flags().StringVarP(&flags.dest, "dest", "d", "", "Destination path (default: ./<agent-name>/ or ./<agent-name>.zip)")
	cmd.Flags().BoolVar(&flags.zip, "zip", false, "Save as zip file instead of extracting")

	return cmd
}

func (a *CodeDownloadAction) Run(ctx context.Context) error {
	agentClient, err := a.NewClient()
	if err != nil {
		return err
	}

	// Determine output path
	outputPath := a.flags.dest
	if outputPath == "" {
		if a.flags.zip {
			outputPath = a.Name + ".zip"
		} else {
			outputPath = a.Name
		}
	}

	// Check if output path already exists
	if _, statErr := os.Stat(outputPath); statErr == nil {
		return fmt.Errorf(
			"output path %q already exists\n\nUse --dest (-d) to specify a different path",
			outputPath,
		)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("failed to check output path %q: %w", outputPath, statErr)
	}

	// Download
	result, err := agentClient.DownloadAgentCode(
		ctx,
		a.Name,
		DefaultAgentAPIVersion,
		a.flags.version,
	)
	if err != nil {
		return fmt.Errorf("failed to download agent code: %w", err)
	}
	defer result.Body.Close()

	if a.flags.zip {
		// Save zip directly
		if err := saveZipFile(outputPath, result.Body, result.ContentHash); err != nil {
			return err
		}
	} else {
		// Save to temp file, verify hash, then extract
		if err := downloadAndExtract(outputPath, result.Body, result.ContentHash); err != nil {
			return err
		}
	}

	// Print summary
	versionStr := result.AgentVersion
	if versionStr == "" {
		versionStr = "latest"
	}
	fmt.Printf("Downloaded agent %q (version %s) -> %s\n", a.Name, versionStr, outputPath)
	if result.ContentHash != "" {
		fmt.Printf("SHA-256: %s\n", result.ContentHash)
	}

	return nil
}

// saveZipFile writes the response body to a zip file and optionally verifies the hash.
func saveZipFile(outputPath string, body io.Reader, expectedHash string) error {
	//nolint:gosec // G304: outputPath is provided by the user via CLI flag
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file %q: %w", outputPath, err)
	}

	hasher := sha256.New()
	writer := io.MultiWriter(outFile, hasher)

	if _, err := io.Copy(writer, body); err != nil {
		// Clean up partial file
		_ = outFile.Close()
		_ = os.Remove(outputPath)
		return fmt.Errorf("failed to write zip file: %w", err)
	}

	if expectedHash != "" {
		actualHash := hex.EncodeToString(hasher.Sum(nil))
		if !equalFoldHash(actualHash, expectedHash) {
			_ = outFile.Close()
			_ = os.Remove(outputPath)
			return fmt.Errorf(
				"SHA-256 verification failed: expected %s, got %s",
				expectedHash, actualHash,
			)
		}
	}

	if err := outFile.Close(); err != nil {
		_ = os.Remove(outputPath)
		return fmt.Errorf("failed to close output file %q: %w", outputPath, err)
	}

	return nil
}

// downloadAndExtract saves the body to a temp file, verifies hash, and extracts to outputDir.
func downloadAndExtract(outputDir string, body io.Reader, expectedHash string) error {
	// Write to temp file
	tmpFile, err := os.CreateTemp("", "azd-agent-code-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	hasher := sha256.New()
	writer := io.MultiWriter(tmpFile, hasher)

	if _, err := io.Copy(writer, body); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("failed to download zip: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Verify hash
	if expectedHash != "" {
		actualHash := hex.EncodeToString(hasher.Sum(nil))
		if !equalFoldHash(actualHash, expectedHash) {
			return fmt.Errorf(
				"SHA-256 verification failed: expected %s, got %s",
				expectedHash, actualHash,
			)
		}
	}

	// Extract zip
	return extractZip(tmpPath, outputDir)
}

// extractZip extracts a zip archive to the specified directory.
func extractZip(zipPath, outputDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("failed to resolve output directory: %w", err)
	}

	for _, f := range r.File {
		if err := extractZipEntry(f, absOutput); err != nil {
			return err
		}
	}

	return nil
}

// extractZipEntry extracts a single zip entry to the output directory with zip-slip protection.
func extractZipEntry(f *zip.File, absOutput string) error {
	// Resolve absolute path of target and verify it stays under absOutput.
	//nolint:gosec // G305: zip-slip is checked via filepath.Rel below
	target := filepath.Join(absOutput, f.Name)
	rel, err := filepath.Rel(absOutput, target)
	if err != nil || rel == ".." || len(rel) > 2 && rel[:3] == ".."+string(os.PathSeparator) {
		return fmt.Errorf("invalid file path in zip (zip-slip detected): %s", f.Name)
	}

	if f.FileInfo().IsDir() {
		if err := os.MkdirAll(target, 0750); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", target, err)
		}
		return nil
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return fmt.Errorf("failed to create parent directory for %q: %w", target, err)
	}

	//nolint:gosec // G304: target path is validated above via filepath.Rel
	outFile, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return fmt.Errorf("failed to create file %q: %w", target, err)
	}

	rc, err := f.Open()
	if err != nil {
		_ = outFile.Close()
		return fmt.Errorf("failed to open zip entry %q: %w", f.Name, err)
	}

	//nolint:gosec // G110: controlled extraction with zip-slip protection above
	if _, err := io.Copy(outFile, rc); err != nil {
		_ = rc.Close()
		_ = outFile.Close()
		return fmt.Errorf("failed to extract %q: %w", f.Name, err)
	}

	_ = rc.Close()
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("failed to close extracted file %q: %w", target, err)
	}

	return nil
}

// equalFoldHash compares two hex-encoded hash strings case-insensitively.
func equalFoldHash(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
