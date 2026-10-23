// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/installer"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// installScriptURL is the PowerShell install script for azd on Windows.
const installScriptURL = "https://aka.ms/install-azd.ps1"

// msiConfigurationURL documents the MSI properties that control install scope and location.
const msiConfigurationURL = "https://github.com/Azure/azure-dev/blob/main/cli/installer/README.md#msi-configuration"

// expectedPerUserInstallDir is the default per-user MSI install directory (ALLUSERS=2).
func expectedPerUserInstallDir() string {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return ""
	}
	return filepath.Join(localAppData, "Programs", "Azure Dev CLI")
}

// backupExe moves the running image aside and leaves an unlocked safety copy for
// MSI to replace. Windows keeps the renamed image alive, and the safety copy
// leaves a working azd.exe if the update is interrupted.
//
// An empty backupRoot uses the user's temporary directory.
// Machine installations use the install directory to preserve its volume and inherited ACL.
func backupExe(originalPath, backupRoot string) (backupPath string, err error) {
	tmpDir, err := os.MkdirTemp(backupRoot, "azd-update-backup")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory for backup: %w", err)
	}

	backupPath = filepath.Join(tmpDir, filepath.Base(originalPath))

	if err := os.Rename(originalPath, backupPath); err != nil {
		_ = os.Remove(tmpDir)
		return "", fmt.Errorf("failed to rename executable for backup: %w", err)
	}

	if err := copyFileWindows(backupPath, originalPath); err != nil {
		// Copy failed — restore the rename so we don't leave a broken state.
		_ = os.Rename(backupPath, originalPath)
		_ = os.Remove(tmpDir)
		return "", fmt.Errorf("failed to create safety copy of executable: %w", err)
	}

	log.Printf("Backed up %s -> %s", originalPath, backupPath)
	return backupPath, nil
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

	_ = os.RemoveAll(filepath.Dir(backupPath))
	log.Printf("Restored executable from backup: %s -> %s", backupPath, originalPath)
	return nil
}

// msiInstallation describes the MSI-installed azd that is currently running.
type msiInstallation struct {
	exePath string
	// perMachine is set when the executable directory matches the machine registration
	// written by an ALLUSERS=1 installation.
	perMachine bool
}

// machineMSIInstallDir returns the directory registered by a machine-wide (ALLUSERS=1)
// MSI installation, or "" when none is registered.
//
// Release MSIs are x64 only and register under the 64-bit registry view, so that view
// is read explicitly regardless of the running process architecture.
func machineMSIInstallDir() (string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Azure Dev CLI`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading machine MSI registration: %w", err)
	}
	defer key.Close()

	dir, _, err := key.GetStringValue("InstallDir")
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading machine MSI installation directory: %w", err)
	}
	return dir, nil
}

// currentMSIInstallation resolves the running executable against the registered machine
// installation and the default per-user installation directory.
func currentMSIInstallation() (*msiInstallation, error) {
	exePath, err := currentExePath()
	if err != nil {
		return nil, err
	}
	machineDir, err := machineMSIInstallDir()
	if err != nil {
		return nil, newUpdateError(CodeNonStandardInstall, err)
	}
	if machineDir != "" {
		machineDir, err = filepath.EvalSymlinks(machineDir)
		if os.IsNotExist(err) {
			machineDir = ""
		} else if err != nil {
			return nil, newUpdateError(CodeNonStandardInstall,
				fmt.Errorf("resolving machine MSI installation directory: %w", err))
		}
	}
	userDir := expectedPerUserInstallDir()
	log.Printf("MSI installation: executable %s, registered machine directory %q, per-user directory %q",
		exePath, machineDir, userDir)
	return resolveMSIInstallation(exePath, machineDir, userDir)
}

func resolveMSIInstallation(exePath, machineDir, userDir string) (*msiInstallation, error) {
	actualDir := filepath.Dir(exePath)
	if machineDir != "" && strings.EqualFold(filepath.Clean(actualDir), filepath.Clean(machineDir)) {
		return &msiInstallation{exePath: exePath, perMachine: true}, nil
	}
	if userDir != "" && strings.EqualFold(filepath.Clean(actualDir), filepath.Clean(userDir)) {
		return &msiInstallation{exePath: exePath}, nil
	}

	return nil, &UpdateError{Code: CodeNonStandardInstall, Err: &internal.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"azd at %s is not a registered machine installation or the default per-user installation", actualDir),
		Suggestion: "Use the original installer to update azd, or contact your administrator. " +
			"To suppress update notifications, set AZD_SKIP_UPDATE_CHECK=1.",
		Links: []errorhandler.ErrorLink{{URL: msiConfigurationURL, Title: "MSI configuration"}},
	}}
}

// validate reports whether the running process can update this installation to target.
// Per-user installations always qualify. Machine-wide installations require the x64
// stable MSI and an already elevated process: azd never requests UAC elevation itself.
func (install *msiInstallation) validate(target *VersionInfo, currentChannel Channel, elevated bool) error {
	if !install.perMachine {
		return nil
	}
	if runtime.GOARCH != "amd64" {
		return newUpdateErrorf(CodeUnsupportedInstallMethod,
			"machine-wide MSI updates require x64 azd; no release MSI is published for %s", runtime.GOARCH)
	}
	if currentChannel != ChannelStable || target.Channel != ChannelStable {
		return newUpdateErrorf(CodeUnsupportedInstallMethod,
			"machine-wide updates support stable-to-stable updates only; "+
				"use the MSI installer to change channels")
	}
	if !elevated {
		return &UpdateError{Code: CodeElevationRequired, Err: &internal.ErrorWithSuggestion{
			Err: fmt.Errorf("updating the machine-wide azd installation at %s requires an elevated terminal",
				filepath.Dir(install.exePath)),
			Suggestion: fmt.Sprintf(
				"Open a terminal with 'Run as administrator' and run %s, or contact your administrator.",
				output.WithHighLightFormat("azd update --channel stable")),
		}}
	}
	return nil
}

// lockMSIInstallation holds a global named mutex for dir so that concurrent azd processes
// cannot update the same installation at once.
func lockMSIInstallation(dir string) (func(), error) {
	hash := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(dir))))
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(`Global\AzureDevCLIUpdate-%x`, hash))
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if handle != 0 && err != nil {
		windows.CloseHandle(handle)
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, fmt.Errorf("another azd update is already running for %s", dir)
	}
	if err != nil {
		return nil, fmt.Errorf("locking azd installation: %w", err)
	}
	return func() { windows.CloseHandle(handle) }, nil
}

// systemPowerShell returns the path of Windows PowerShell 5.1 in the system directory,
// which is used instead of whatever "powershell" resolves to on PATH.
func systemPowerShell() (string, error) {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("locating system PowerShell: %w", err)
	}
	return filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe"), nil
}

func (m *Manager) verifyAuthenticode(ctx context.Context, binaryPath string, writer io.Writer) error {
	powershell, err := systemPowerShell()
	if err != nil {
		return newUpdateError(CodeSignatureInvalid, err)
	}
	// Reset PSModulePath so PowerShell 5.1 does not inherit PowerShell 7 module paths
	// (see buildInstallScriptArgs).
	script := fmt.Sprintf(
		`$env:PSModulePath = Join-Path $PSHOME 'Modules'; `+
			`$sig = Get-AuthenticodeSignature -LiteralPath '%s'; if ($sig.Status -ne 'Valid') { `+
			`Write-Error "Signature status: $($sig.Status)"; exit 1 }`,
		escapeForPSSingleQuote(binaryPath),
	)
	runArgs := exec.NewRunArgs(powershell, "-NoProfile", "-Command", script).WithShell(false)
	result, err := m.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return newUpdateError(CodeSignatureInvalid, fmt.Errorf("Authenticode verification failed: %w", err))
	}
	if result.ExitCode != 0 {
		return newUpdateErrorf(CodeSignatureInvalid,
			"Authenticode signature verification failed for %s: %s", binaryPath, result.Stderr)
	}
	fmt.Fprintf(writer, "Code signature verified.\n")
	return nil
}

func (m *Manager) updateViaMSI(ctx context.Context, target *VersionInfo, writer io.Writer) error {
	install, err := currentMSIInstallation()
	if err != nil {
		return err
	}
	elevated := windows.GetCurrentProcessToken().IsElevated()
	currentChannel := inferChannelFromVersion()
	log.Printf("MSI update: per-machine %t, elevated %t, arch %s, channel %s -> %s, version %s -> %s",
		install.perMachine, elevated, runtime.GOARCH, currentChannel, target.Channel,
		internal.Version, target.installVersion())
	if err := install.validate(target, currentChannel, elevated); err != nil {
		return err
	}
	return m.updateMSI(ctx, target, install, writer)
}

// updateMSI replaces install with target. Per-user installations run install-azd.ps1, which
// downloads and installs the pinned MSI. Machine-wide installations download and verify the
// pinned MSI here and run it with ALLUSERS=1 against the existing installation directory.
//
// The running executable is renamed aside and copied back unlocked so the installer can
// replace it while this process keeps running from the renamed image. Installer failures
// restore the executable, but a failed major upgrade may also remove the old machine
// registration and require reinstallation. Once a machine MSI transaction has committed,
// azd.exe is never rolled back on its own; verification failures retain the backup for repair.
func (m *Manager) updateMSI(
	ctx context.Context, target *VersionInfo, install *msiInstallation, writer io.Writer,
) (err error) {
	installDir := filepath.Dir(install.exePath)
	unlock, err := lockMSIInstallation(installDir)
	if err != nil {
		return newUpdateError(CodeReplaceFailed, err)
	}
	defer unlock()

	var runArgs exec.RunArgs
	if install.perMachine {
		var cleanupPackage func(keepLog bool)
		runArgs, cleanupPackage, err = m.prepareMachineMSI(ctx, target, installDir, writer)
		if err != nil {
			return err
		}
		// Keep the installer log for every non-success outcome, including a required restart.
		defer func() { cleanupPackage(err != nil) }()
	} else {
		psArgs, err := buildInstallScriptArgs(target)
		if err != nil {
			return newUpdateError(CodeReplaceFailed, err)
		}
		runArgs = exec.NewRunArgs("powershell", psArgs...)
	}
	runArgs = runArgs.WithStdOut(writer).WithStdErr(writer)

	// Honor cancellation before touching the installation; the installer itself runs to completion.
	if err := ctx.Err(); err != nil {
		return newUpdateError(CodeReplaceFailed, err)
	}
	backupRoot := ""
	if install.perMachine {
		backupRoot = installDir
	}
	fmt.Fprintf(writer, "Backing up current azd executable...\n")
	backupPath, err := backupExe(install.exePath, backupRoot)
	if err != nil {
		return newUpdateError(CodeReplaceFailed, err)
	}
	// committed: the installer has committed the new package, so azd.exe is never restored.
	// retainBackup: committed but unverified, so the original is kept for manual repair.
	committed := false
	retainBackup := false
	defer func() {
		if !committed {
			fmt.Fprintf(writer, "Restoring previous executable...\n")
			if err := restoreExeFromBackup(install.exePath, backupPath); err != nil {
				fmt.Fprintf(writer, "WARNING: could not restore previous version: %v\n", err)
				fmt.Fprintf(writer, "Copy the backup at %s to %s to recover manually.\n", backupPath, install.exePath)
				return
			}
		} else if retainBackup {
			fmt.Fprintf(writer, "Original executable retained at %s.\n", backupPath)
			return
		}
		cleanMSIBackup(backupPath, writer)
	}()

	preHash, err := hashFile(install.exePath)
	if err != nil {
		return newUpdateError(CodeReplaceFailed, fmt.Errorf("hashing current executable: %w", err))
	}
	log.Printf("Executable hash before install: %s", preHash)
	fmt.Fprintf(writer, "Installing azd %s channel...\n", target.Channel)
	installCtx := ctx
	if install.perMachine {
		// Let the MSI transaction settle before restoring anything. Canceling the
		// msiexec client process does not reliably stop Windows Installer.
		installCtx = context.WithoutCancel(ctx)
	}
	result, err := m.commandRunner.Run(installCtx, runArgs)
	// Windows Installer reports 3010 as success with a restart required, even though the
	// command runner returns an exit error. The committed installation must not be rolled back.
	if install.perMachine && result.ExitCode == int(windows.ERROR_SUCCESS_REBOOT_REQUIRED) {
		log.Printf("MSI committed with exit code %d; Windows restart required", result.ExitCode)
		committed = true
		return ErrRestartRequired
	}
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("Windows Installer exited with code %d", result.ExitCode)
	}
	if err != nil {
		err = fmt.Errorf("MSI update failed: %w", err)
		if install.perMachine {
			// Existing release MSIs remove the old product before the rollback transaction.
			// Copying azd.exe back cannot restore registration or the machine PATH.
			err = &internal.ErrorWithSuggestion{
				Err: err,
				Suggestion: fmt.Sprintf(
					"A failed upgrade may remove the previous machine installation. "+
						"Restoring azd.exe does not restore MSI registration or the system PATH. "+
						"Reinstall the stable MSI with ALLUSERS=1 and INSTALLDIR=\"%s\" from an elevated terminal.",
					installDir),
				Links: []errorhandler.ErrorLink{{URL: msiConfigurationURL, Title: "MSI configuration"}},
			}
		}
		return newUpdateError(CodeReplaceFailed, err)
	}
	// A machine MSI has committed its registration; the per-user script path is only
	// trusted once the executable is verified below.
	committed = install.perMachine
	postHash, err := hashFile(install.exePath)
	if err != nil {
		retainBackup = true
		return newUpdateError(CodeReplaceFailed,
			fmt.Errorf("MSI completed but the installed executable could not be read: %w. "+
				"Run the MSI installer to repair the installation", err))
	}
	log.Printf("Executable hash after install: %s", postHash)
	if preHash == postHash {
		retainBackup = true
		return newUpdateErrorf(CodeReplaceFailed,
			"MSI completed but the binary at %s was not updated. Run the MSI installer to repair the installation",
			install.exePath)
	}
	committed = true
	return nil
}

// prepareMachineMSI downloads and verifies the pinned stable MSI for a machine-wide update
// and returns the msiexec invocation that installs it over installDir. The returned cleanup
// removes the downloaded package and, unless keepLog is set, the installer log directory.
func (m *Manager) prepareMachineMSI(
	ctx context.Context, target *VersionInfo, installDir string, writer io.Writer,
) (runArgs exec.RunArgs, cleanup func(keepLog bool), err error) {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return runArgs, nil, newUpdateError(CodeReplaceFailed, fmt.Errorf("locating Windows Installer: %w", err))
	}
	// Keep elevated installer inputs out of a user-writable temporary directory.
	packageDir, err := os.MkdirTemp(installDir, "azd-update-msi-")
	if err != nil {
		return runArgs, nil, newUpdateError(CodeDownloadFailed, err)
	}
	packagePath := filepath.Join(packageDir, "azd.msi")
	logPath := filepath.Join(packageDir, "install.log")
	cleanup = func(keepLog bool) {
		os.Remove(packagePath)
		if _, statErr := os.Stat(logPath); keepLog && statErr == nil {
			fmt.Fprintf(writer, "MSI log: %s\n", logPath)
			return
		}
		log.Printf("Removing installer directory %s", packageDir)
		if removeErr := os.RemoveAll(packageDir); removeErr != nil {
			fmt.Fprintf(writer, "WARNING: could not remove installer directory %s: %v\n", packageDir, removeErr)
		}
	}

	url := fmt.Sprintf("%s/%s/azd-windows-%s.msi", blobBaseURL, target.installVersion(), runtime.GOARCH)
	log.Printf("Downloading %s to %s", url, packagePath)
	if err := m.downloadFile(ctx, url, packagePath, writer); err != nil {
		cleanup(false)
		return runArgs, nil, newUpdateError(CodeDownloadFailed, err)
	}
	if err := m.verifyAuthenticode(ctx, packagePath, writer); err != nil {
		cleanup(false)
		return runArgs, nil, err
	}

	runArgs = exec.NewRunArgs(filepath.Join(systemDir, "msiexec.exe"),
		"/i", packagePath, "/qn", "/norestart", "/L*v", logPath,
		"ALLUSERS=1", "INSTALLDIR="+installDir, "MSIRESTARTMANAGERCONTROL=DisableShutdown").
		WithShell(false)
	if installer.InstalledBy() == installer.InstallTypePs {
		runArgs = runArgs.AppendParams("INSTALLEDBY=" + string(installer.InstallTypePs))
	}
	runArgs.WindowsCommandLine = msiCommandLine(runArgs.Cmd, runArgs.Args)
	return runArgs, cleanup, nil
}

func msiCommandLine(command string, args []string) string {
	parts := []string{syscall.EscapeArg(command)}
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "INSTALLDIR="); ok {
			// MSI requires PROPERTY="value", not Go's default "PROPERTY=value".
			parts = append(parts, `INSTALLDIR="`+value+`"`)
		} else {
			parts = append(parts, syscall.EscapeArg(arg))
		}
	}
	return strings.Join(parts, " ")
}

// hashFile returns the hex-encoded SHA-256 digest of the file at path.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func escapeForPSSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// buildInstallScriptArgs constructs the PowerShell arguments to run install-azd.ps1
// to install target.
// For all channels, the script is downloaded to a temp directory.
// The version passed to the script is chosen by [VersionInfo.installVersion].
// For daily channel, an additional parameter (-InstallFolder) is passed
// to the script. The install folder and version are escaped for PowerShell single-quoted
// strings to handle values containing apostrophes (e.g. O'Connor).
// An unsupported channel is an error rather than a fallback: defaulting to the rolling
// stable folder would produce the unpinned install this exists to prevent.
// Returns the arguments to pass to the "powershell" command.
func buildInstallScriptArgs(target *VersionInfo) ([]string, error) {
	var scriptArgs string
	switch target.Channel {
	case ChannelDaily:
		scriptArgs = fmt.Sprintf(" -Version 'daily' -InstallFolder '%s'",
			escapeForPSSingleQuote(expectedPerUserInstallDir()))
	case ChannelStable:
		// Pin stable to the resolved version so the MSI comes from the immutable
		// release/<version>/ folder rather than the rolling release/stable/ folder,
		// which can already hold a newer build (Azure/azure-dev#9145).
		scriptArgs = fmt.Sprintf(" -Version '%s'",
			escapeForPSSingleQuote(target.installVersion()))
	default:
		return nil, fmt.Errorf("unsupported channel: %s", target.Channel)
	}

	// Reset PSModulePath to the Windows PowerShell 5.1 system modules directory.
	// When launched via cmd.exe from a PowerShell 7 parent (e.g. VSCode or Windows Terminal),
	// PS5.1 inherits PS7's PSModulePath which includes Core-edition modules that fail to load
	// in the Desktop edition runtime, causing Get-AuthenticodeSignature to fail with
	// "CouldNotAutoloadMatchingModule".
	resetPSModulePath := "$env:PSModulePath = Join-Path $PSHOME 'Modules'; "

	script := fmt.Sprintf(
		resetPSModulePath+
			"$tmpScript = Join-Path $env:TEMP 'azd-install.ps1'; "+
			"Invoke-RestMethod '%s' -OutFile $tmpScript; "+
			"& $tmpScript%s; "+
			"Remove-Item $tmpScript -Force -ErrorAction SilentlyContinue",
		installScriptURL, scriptArgs,
	)
	return []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", script}, nil
}
