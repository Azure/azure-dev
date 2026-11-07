// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"fmt"
	"io"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// backupCleanupAttempts bounds how long the detached cleanup helper retries, at one
// attempt per second, before it gives up and leaves the backup in place.
const backupCleanupAttempts = 60

// cleanMSIBackup removes the executable backup left by backupExe. The backup is usually
// still the running image, so when it cannot be removed directly a detached helper keeps
// retrying after azd exits.
func cleanMSIBackup(backupPath string, writer io.Writer) {
	err := removeMSIBackup(backupPath)
	if err == nil {
		log.Printf("Removed executable backup %s", backupPath)
		return
	}
	log.Printf("Executable backup %s is still in use (%v); starting detached cleanup", backupPath, err)

	// The helper must outlive azd and its command job, without adding
	// system-wide pending reboot operations.
	cmd, err := msiBackupCleanupCommand(backupPath, backupCleanupAttempts)
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		fmt.Fprintf(writer, "WARNING: could not start executable backup cleanup: %v\n", err)
		fmt.Fprintf(writer, "After azd exits, remove the backup at %s manually.\n", backupPath)
		return
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Release(); err != nil {
		log.Printf("could not release the backup cleanup process: %v", err)
	}
	log.Printf("Backup cleanup helper (pid %d) continues after azd exits; failures are written to %s",
		pid, filepath.Join(filepath.Dir(backupPath), "cleanup.log"))
}

func removeMSIBackup(backupPath string) error {
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(filepath.Dir(backupPath)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// msiBackupCleanupCommand builds a hidden system PowerShell process that deletes the backup
// executable and its directory, retrying once per second for the given number of attempts.
// The last failure is written to cleanup.log beside the backup.
func msiBackupCleanupCommand(backupPath string, attempts int) (*osexec.Cmd, error) {
	powershell, err := systemPowerShell()
	if err != nil {
		return nil, err
	}
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
$backup = '%s'
$directory = '%s'
$log = [System.IO.Path]::Combine($directory, 'cleanup.log')
for ($attempt = 0; $attempt -lt %d; $attempt++) {
    if (![System.IO.Directory]::Exists($directory)) { exit 0 }
    try {
        [System.IO.File]::Delete($backup)
        [System.IO.File]::Delete($log)
        [System.IO.Directory]::Delete($directory)
        exit 0
    } catch {
        [System.IO.File]::WriteAllText($log, $_.Exception.ToString())
    }
    if ($attempt -lt %d) { [System.Threading.Thread]::Sleep(1000) }
}
exit 1
`, escapeForPSSingleQuote(backupPath), escapeForPSSingleQuote(filepath.Dir(backupPath)), attempts, attempts-1)

	//nolint:gosec // Executes system PowerShell with a script limited to the generated backup path.
	cmd := osexec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_BREAKAWAY_FROM_JOB,
	}
	return cmd, nil
}
