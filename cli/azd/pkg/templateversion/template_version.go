// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templateversion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

const (
	// VersionFileName is the name of the file that contains the template version
	VersionFileName = "AZD_TEMPLATE_VERSION"

	// ReadOnlyFilePerms sets the file as read-only
	ReadOnlyFilePerms = 0444
)

// VersionInfo represents the parsed template version information
type VersionInfo struct {
	// Date in YYYY-MM-DD format
	Date string `json:"date"`

	// CommitHash is the short git commit hash
	CommitHash string `json:"commit_hash"`

	// FullVersion is the complete version string (YYYY-MM-DD-<short-git-hash>)
	FullVersion string `json:"full_version"`
}

// Manager provides operations for template versioning
type Manager struct {
	console input.Console
	runner  exec.CommandRunner
}

// NewManager creates a new template version manager
func NewManager(console input.Console, runner exec.CommandRunner) *Manager {
	return &Manager{
		console: console,
		runner:  runner,
	}
}

// GetShortCommitHash returns the short commit hash for the current repository
func (m *Manager) GetShortCommitHash(ctx context.Context, projectPath string) (string, error) {
	// First check if git is initialized in the directory
	checkArgs := exec.RunArgs{
		Cmd:  "git",
		Args: []string{"rev-parse", "--is-inside-work-tree"},
	}

	// Set working directory
	checkArgs = checkArgs.WithCwd(projectPath)

	// Check if we're in a git repository
	_, checkErr := m.runner.Run(ctx, checkArgs)
	if checkErr != nil {
		// Not in a git repository or git not installed, return a fallback hash
		return "dev", nil
	}

	// Get the short commit hash
	args := exec.RunArgs{
		Cmd:  "git",
		Args: []string{"rev-parse", "--short", "HEAD"},
	}

	// Set working directory
	args = args.WithCwd(projectPath)

	result, err := m.runner.Run(ctx, args)
	if err != nil {
		// If we can't get the hash, use a fallback
		return "unknown", nil
	}

	hash := strings.TrimSpace(result.Stdout)
	if hash == "" {
		return "unknown", nil
	}

	return hash, nil
}

// CreateVersionFile creates the AZD_TEMPLATE_VERSION file with the current date and git commit hash
func (m *Manager) CreateVersionFile(ctx context.Context, projectPath string) (string, error) {
	// Get current date in YYYY-MM-DD format
	currentDate := time.Now().Format("2006-01-02")

	// Get the git short commit hash
	commitHash, err := m.GetShortCommitHash(ctx, projectPath)
	if err != nil {
		m.console.Message(ctx, fmt.Sprintf("WARNING: Error getting git hash: %v, using fallback", err))
		commitHash = "dev"
	}

	// Create the version string
	versionString := fmt.Sprintf("%s-%s", currentDate, commitHash)

	// Create the file path
	filePath := filepath.Join(projectPath, VersionFileName)
	m.console.Message(ctx, fmt.Sprintf("DEBUG: Creating version file at: %s with content: %s", filePath, versionString))

	// Check if the file already exists and remove it if needed
	if _, err := os.Stat(filePath); err == nil {
		// File exists, try to make it writable before removing
		m.console.Message(ctx, "DEBUG: File already exists, attempting to make writable before removing")
		err = os.Chmod(filePath, 0666)
		if err != nil {
			m.console.Message(ctx, fmt.Sprintf("WARNING: Failed to change file permissions: %v", err))
			// Continue anyway, as removal might still work
		}

		err = os.Remove(filePath)
		if err != nil {
			m.console.Message(ctx, fmt.Sprintf("ERROR: Failed to remove existing version file: %v", err))
			return "", fmt.Errorf("failed to remove existing version file %s: %w", filePath, err)
		}
	}

	// Write the file with read-only permissions
	m.console.Message(ctx, "DEBUG: Writing version file with read-only permissions")
	err = os.WriteFile(filePath, []byte(versionString), ReadOnlyFilePerms)
	if err != nil {
		// Check if the error is due to permissions
		if os.IsPermission(err) {
			m.console.Message(ctx, fmt.Sprintf("ERROR: Permission denied creating file %s", filePath))
			return "", fmt.Errorf("permission denied creating version file %s: %w", filePath, err)
		}

		m.console.Message(ctx, fmt.Sprintf("ERROR: Failed to write version file: %v", err))
		return "", fmt.Errorf("failed to create version file %s: %w", filePath, err)
	}

	// Verify the file was created
	if _, statErr := os.Stat(filePath); statErr != nil {
		m.console.Message(ctx, fmt.Sprintf("ERROR: File creation verification failed: %v", statErr))
	} else {
		m.console.Message(ctx, "DEBUG: File creation verification succeeded")
	}

	m.console.Message(ctx, fmt.Sprintf("Created template version file at %s: %s", filePath, versionString))
	m.console.Message(ctx, "Please commit this file to your repository.")

	return versionString, nil
}

// ReadVersionFile reads the AZD_TEMPLATE_VERSION file and returns the version string
func (m *Manager) ReadVersionFile(projectPath string) (string, error) {
	filePath := filepath.Join(projectPath, VersionFileName)

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // File doesn't exist, not an error
		}

		// Check if the error is due to permissions
		if os.IsPermission(err) {
			return "", fmt.Errorf("permission denied reading version file %s: %w", filePath, err)
		}

		return "", fmt.Errorf("failed to read version file %s: %w", filePath, err)
	}

	version := strings.TrimSpace(string(data))
	if version == "" {
		return "", nil // Empty file, treat as non-existent
	}

	return version, nil
}

// ParseVersionString parses a version string into a VersionInfo
func ParseVersionString(version string) (*VersionInfo, error) {
	if version == "" {
		return nil, fmt.Errorf("empty version string")
	}

	parts := strings.Split(version, "-")

	// Version should be in format YYYY-MM-DD-hash, so we need at least 4 parts
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid version string format: %s, expected YYYY-MM-DD-hash", version)
	}

	// Validate date format (YYYY-MM-DD)
	dateStr := strings.Join(parts[:3], "-")
	_, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid date format in version string: %s, expected YYYY-MM-DD", dateStr)
	}

	// Commit hash is the last part (or all remaining parts joined if there are more than 4)
	commitHash := strings.Join(parts[3:], "-")
	if commitHash == "" {
		return nil, fmt.Errorf("missing commit hash in version string: %s", version)
	}

	return &VersionInfo{
		Date:        dateStr,
		CommitHash:  commitHash,
		FullVersion: version,
	}, nil
}

// EnsureTemplateVersion ensures that the AZD_TEMPLATE_VERSION file exists
// If it doesn't exist, it creates it and returns the version string
// If it does exist, it reads the version string and returns it
func (m *Manager) EnsureTemplateVersion(ctx context.Context, projectPath string) (string, error) {
	// Print a debug message about the project path
	m.console.Message(ctx, fmt.Sprintf("Ensuring template version for project path: %s", projectPath))

	if projectPath == "" {
		m.console.Message(ctx, "ERROR: Project path is empty")
		return "", fmt.Errorf("project path cannot be empty")
	}

	// Check if project path exists
	_, err := os.Stat(projectPath)
	if err != nil {
		if os.IsNotExist(err) {
			m.console.Message(ctx, fmt.Sprintf("ERROR: Project path does not exist: %s", projectPath))
			return "", fmt.Errorf("project path does not exist: %s", projectPath)
		}
		m.console.Message(ctx, fmt.Sprintf("ERROR: Failed to access project path: %v", err))
		return "", fmt.Errorf("failed to access project path: %w", err)
	}

	// Check if the project path is a git repository
	checkArgs := exec.RunArgs{
		Cmd:  "git",
		Args: []string{"rev-parse", "--is-inside-work-tree"},
		Cwd:  projectPath,
	}
	_, checkErr := m.runner.Run(ctx, checkArgs)
	if checkErr != nil {
		m.console.Message(ctx, fmt.Sprintf("DEBUG: Not a git repository or git error: %v", checkErr))
	} else {
		m.console.Message(ctx, "DEBUG: Confirmed path is a git repository")
	}

	// Try to read the version file
	versionFilePath := filepath.Join(projectPath, VersionFileName)
	m.console.Message(ctx, fmt.Sprintf("DEBUG: Checking for version file at: %s", versionFilePath))
	
	version, err := m.ReadVersionFile(projectPath)
	if err != nil {
		// Log the error but continue with creating a new file
		m.console.Message(ctx, fmt.Sprintf("Warning: Failed to read version file: %v", err))
		version = ""
	}

	// If the file doesn't exist or is empty, create it
	if version == "" {
		m.console.Message(ctx, "DEBUG: Version file doesn't exist or is empty, creating it now")
		createdVersion, err := m.CreateVersionFile(ctx, projectPath)
		if err != nil {
			m.console.Message(ctx, fmt.Sprintf("ERROR: Failed to create version file: %v", err))
			return "", fmt.Errorf("failed to create template version file: %w", err)
		}
		m.console.Message(ctx, fmt.Sprintf("DEBUG: Successfully created version file with: %s", createdVersion))
		return createdVersion, nil
	} else {
		m.console.Message(ctx, fmt.Sprintf("DEBUG: Found existing version: %s", version))
	}

	// Validate the existing version format
	_, err = ParseVersionString(version)
	if err != nil {
		m.console.Message(ctx, fmt.Sprintf("Warning: Invalid version format in %s: %v", VersionFileName, err))
		m.console.Message(ctx, "Creating a new version file with the correct format...")

		// Rename the old file to preserve it
		oldPath := filepath.Join(projectPath, VersionFileName)
		backupPath := filepath.Join(projectPath, VersionFileName+".bak")

		// Try to rename, but continue even if it fails
		if renameErr := os.Rename(oldPath, backupPath); renameErr != nil {
			m.console.Message(ctx, fmt.Sprintf("DEBUG: Failed to rename old version file: %v", renameErr))
		}

		// Create a new file with the correct format
		createdVersion, err := m.CreateVersionFile(ctx, projectPath)
		if err != nil {
			m.console.Message(ctx, fmt.Sprintf("ERROR: Failed to create version file with correct format: %v", err))
			return "", fmt.Errorf("failed to create template version file: %w", err)
		}
		return createdVersion, nil
	}

	return version, nil
}
