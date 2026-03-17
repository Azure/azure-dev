// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
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

// currentExePath returns the resolved path of the currently running executable.
func currentExePath() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to determine current executable path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve executable path: %w", err)
	}
	return resolved, nil
}

// backupCurrentExe renames the currently running azd executable so the MSI installer
// can write a new binary to the original path. Windows allows renaming a file that is
// locked by a running process — the handle follows the renamed file.
// Returns the original path and the backup path.
func backupCurrentExe() (originalPath string, backupPath string, err error) {
	originalPath, err = currentExePath()
	if err != nil {
		return "", "", err
	}

	timestamp := time.Now().Unix()
	backupPath = fmt.Sprintf("%s.old.%d", originalPath, timestamp)

	if err := os.Rename(originalPath, backupPath); err != nil {
		return "", "", fmt.Errorf("failed to rename executable for backup: %w", err)
	}

	log.Printf("Backed up %s -> %s", originalPath, backupPath)
	return originalPath, backupPath, nil
}

// restoreExeFromBackup moves the backup back to the original location.
// This is called when the install script fails so the user is not left without a working binary.
// Returns an error if the restore fails, so the caller can advise the user on manual recovery.
func restoreExeFromBackup(originalPath, backupPath string) error {
	// Remove any partially-installed new binary that might be in the way.
	_ = os.Remove(originalPath)

	if err := os.Rename(backupPath, originalPath); err != nil {
		log.Printf("WARNING: failed to restore executable from backup %s -> %s: %v", backupPath, originalPath, err)
		return fmt.Errorf("failed to restore executable from backup %s -> %s: %w", backupPath, originalPath, err)
	}

	log.Printf("Restored executable from backup: %s -> %s", backupPath, originalPath)
	return nil
}

// cleanupOldBackups removes any leftover azd.exe.old.* files from the install directory.
func cleanupOldBackups(exePath string) {
	dir := filepath.Dir(exePath)
	base := filepath.Base(exePath)
	pattern := filepath.Join(dir, base+".old.*")

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}

	for _, m := range matches {
		if err := os.Remove(m); err != nil {
			log.Printf("warning: failed to remove old backup %s: %v", m, err)
		} else {
			log.Printf("Cleaned up old backup: %s", m)
		}
	}
}

// checkOtherAzdProcesses checks whether other azd.exe processes are running from the same
// executable path, excluding the current process. Returns an error if other instances are found.
func checkOtherAzdProcesses(ctx context.Context, commandRunner exec.CommandRunner) error {
	currentPID := os.Getpid()

	exePath, err := currentExePath()
	if err != nil {
		return err
	}

	// Use PowerShell to find all azd.exe processes and their executable paths.
	// Filter to processes matching our exe path but not our PID.
	script := fmt.Sprintf(
		`Get-Process -Name azd -ErrorAction SilentlyContinue | `+
			`Where-Object { $_.Id -ne %d } | `+
			`Where-Object { try { $_.Path -eq '%s' } catch { $false } } | `+
			`Select-Object -ExpandProperty Id`,
		currentPID, exePath,
	)

	runArgs := exec.NewRunArgs("powershell", "-NoProfile", "-Command", script)
	result, err := commandRunner.Run(ctx, runArgs)
	if err != nil {
		// If the command itself fails, log and allow update to proceed.
		// This avoids blocking updates when process enumeration is restricted.
		log.Printf("warning: failed to check for other azd processes: %v", err)
		return nil
	}

	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		return nil
	}

	// Parse the PIDs from the output to build a helpful message
	var pids []int
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if pid, parseErr := strconv.Atoi(line); parseErr == nil {
			pids = append(pids, pid)
		}
	}

	if len(pids) == 0 {
		return nil
	}

	return newUpdateError(CodeOtherProcessesRunning, fmt.Errorf(
		"found %d other azd process(es) running (PIDs: %v).\n"+
			"Please close all other azd instances before running azd update",
		len(pids), pids,
	))
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
// Get-AuthenticodeSignature can fail in environments where the
// Microsoft.PowerShell.Security module cannot be auto-loaded (a known issue on
// some Windows PowerShell 5.1 configurations). The MSI is already downloaded
// over HTTPS from a Microsoft-controlled domain, so the transport-level
// integrity is sufficient.
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
