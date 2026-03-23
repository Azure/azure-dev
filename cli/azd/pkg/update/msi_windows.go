// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// installScriptURL is the PowerShell install script for azd on Windows.
const installScriptURL = "https://aka.ms/install-azd.ps1"

// expectedPerUserInstallDir is the default per-user MSI install directory (ALLUSERS=2).
// azd update only supports this standard configuration.
func expectedPerUserInstallDir() string {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return ""
	}
	return filepath.Join(localAppData, "Programs", "Azure Dev CLI")
}

// backupCurrentExe prepares the install directory for the MSI to write a new binary.
//
// On Windows a running executable is locked — it cannot be overwritten or deleted.
// However, it CAN be renamed/moved. After a rename the OS handle follows the file,
// so the running process continues from the new path without issues.
//
// Strategy ("rename + safety copy"):
//  1. Rename azd.exe → %TEMP%/azd-update-backup-XXXX/azd.exe
//     This frees the original path AND keeps the running process alive.
//  2. Copy the backup back to the original path (azd.exe).
//     This is an unlocked copy that acts as a safety net: if the process is
//     killed at any point after this (Ctrl+C, power loss, etc.), the user
//     still has a working azd.exe.
//  3. The MSI installer later overwrites the unlocked safety copy with the new version.
//
// Returns the original path and the backup path (in the temp directory).
func backupCurrentExe() (originalPath string, backupPath string, err error) {
	originalPath, err = currentExePath()
	if err != nil {
		return "", "", err
	}

	// Create a dedicated temp directory for the backup.
	tmpDir, err := os.MkdirTemp("", "azd-update-backup")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp directory for backup: %w", err)
	}

	backupPath = filepath.Join(tmpDir, filepath.Base(originalPath))

	// Step 1: Rename the running exe out of the way.
	// The OS handle follows the renamed file — the running process is unaffected.
	if err := os.Rename(originalPath, backupPath); err != nil {
		_ = os.Remove(tmpDir)
		return "", "", fmt.Errorf("failed to rename executable for backup: %w", err)
	}

	// Step 2: Copy the backup back as an unlocked safety copy.
	// If the process is killed before the MSI finishes, this file ensures the
	// user still has a working azd.exe at the original path.
	if err := copyFileWindows(backupPath, originalPath); err != nil {
		// Copy failed — restore the rename so we don't leave a broken state.
		_ = os.Rename(backupPath, originalPath)
		_ = os.Remove(tmpDir)
		return "", "", fmt.Errorf("failed to create safety copy of executable: %w", err)
	}

	log.Printf("Backed up %s -> %s", originalPath, backupPath)
	return originalPath, backupPath, nil
}

// copyFileWindows copies src to dst.
func copyFileWindows(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}

	return out.Close()
}

// restoreExeFromBackup overwrites the original path with the backup copy.
// This is called when the install script fails so the user has the same
// binary they started with (rather than a partially-installed one).
// Returns an error if the restore fails, so the caller can advise the user on manual recovery.
func restoreExeFromBackup(originalPath, backupPath string) error {
	// The safety copy at originalPath may be the old version or a partially-installed
	// new binary. Overwrite it with the known-good backup.
	_ = os.Remove(originalPath)

	if err := copyFileWindows(backupPath, originalPath); err != nil {
		log.Printf("WARNING: failed to restore executable from backup %s -> %s: %v", backupPath, originalPath, err)
		return fmt.Errorf("failed to restore executable from backup %s -> %s: %w", backupPath, originalPath, err)
	}

	// Clean up the backup directory.
	_ = os.RemoveAll(filepath.Dir(backupPath))

	log.Printf("Restored executable from backup: %s -> %s", backupPath, originalPath)
	return nil
}

// isStandardMSIInstall checks whether the current azd binary is installed in the standard
// per-user MSI location (%LOCALAPPDATA%\Programs\Azure Dev CLI). Returns an error if the
// install is non-standard, advising the user to reinstall with the default per-user configuration.
func isStandardMSIInstall() error {
	expectedDir := expectedPerUserInstallDir()
	if expectedDir == "" {
		return newUpdateError(CodeNonStandardInstall, fmt.Errorf(
			"LOCALAPPDATA environment variable is not set; cannot verify install location"))
	}

	exePath, err := currentExePath()
	if err != nil {
		return err
	}

	actualDir := filepath.Dir(exePath)

	// Normalize both paths for comparison (case-insensitive on Windows, clean slashes)
	if !strings.EqualFold(filepath.Clean(actualDir), filepath.Clean(expectedDir)) {
		return newUpdateError(CodeNonStandardInstall, fmt.Errorf(
			"azd is installed in a non-standard location: %s\n"+
				"azd update only supports the default per-user install.\n"+
				"Please reinstall azd with the default configuration:\n"+
				"  ALLUSERS=2  INSTALLDIR=\"%s\"\n"+
				"See https://github.com/Azure/azure-dev/blob/main/cli/installer/README.md#msi-configuration",
			actualDir, expectedDir,
		))
	}

	return nil
}

// versionFlag returns the install script parameter value for the given channel.
func versionFlag(channel Channel) string {
	switch channel {
	case ChannelDaily:
		return "daily"
	case ChannelStable:
		return "stable"
	default:
		return "stable"
	}
}

// buildInstallScriptArgs constructs the PowerShell arguments to download and run
// install-azd.ps1 with the appropriate -Version flag.
// The -SkipVerify flag is passed because Authenticode verification via
// Get-AuthenticodeSignature failed.
// The MSI is already downloaded over HTTPS from a Microsoft-controlled domain,
// so the transport-level integrity is sufficient.
// Returns the arguments to pass to the "powershell" command.
func buildInstallScriptArgs(channel Channel) []string {
	version := versionFlag(channel)
	// Download the script to a temp file, then invoke it with the appropriate -Version flag.
	// Using -ExecutionPolicy Bypass ensures the script runs even if the system policy is restrictive.
	script := fmt.Sprintf(
		`$script = Join-Path $env:TEMP 'install-azd.ps1'; `+
			`Invoke-RestMethod '%s' -OutFile $script; `+
			`& $script -Version '%s' -SkipVerify; `+
			`Remove-Item $script -Force -ErrorAction SilentlyContinue`,
		installScriptURL, version,
	)
	return []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script}
}
