// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/installer"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/blang/semver/v4"
)

const (
	// stableVersionURL is the endpoint that returns the latest stable version.
	stableVersionURL = "https://aka.ms/azure-dev/versions/cli/latest"
	// blobBaseURL is the base URL for Azure Blob Storage where azd binaries are hosted.
	blobBaseURL = "https://azuresdkartifacts.z5.web.core.windows.net/azd/standalone/release"
	// installShScriptURL is the shell install script for azd on Linux/macOS.
	installShScriptURL = "https://aka.ms/install-azd.sh"
)

// VersionInfo holds the result of a version check.
type VersionInfo struct {
	Version     string
	BuildNumber int
	Channel     Channel
	HasUpdate   bool
}

// Manager handles checking for and applying azd updates.
type Manager struct {
	commandRunner exec.CommandRunner
	httpClient    *http.Client
}

// NewManager creates a new update Manager.
func NewManager(commandRunner exec.CommandRunner, httpClient *http.Client) *Manager {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Manager{
		commandRunner: commandRunner,
		httpClient:    httpClient,
	}
}

// CheckForUpdate checks whether a newer version of azd is available.
func (m *Manager) CheckForUpdate(ctx context.Context, cfg *UpdateConfig, ignoreCache bool) (*VersionInfo, error) {
	if !ignoreCache {
		cache, err := LoadCache()
		if err != nil {
			log.Printf("error loading update cache: %v", err)
		}

		if IsCacheValid(cache, cfg.Channel) {
			return m.buildVersionInfoFromCache(cache, cfg.Channel)
		}
	}

	var info *VersionInfo
	var err error

	switch cfg.Channel {
	case ChannelStable:
		info, err = m.checkStableVersion(ctx)
	case ChannelDaily:
		info, err = m.checkDailyVersion(ctx)
	default:
		return nil, fmt.Errorf("unsupported channel: %s", cfg.Channel)
	}

	if err != nil {
		return nil, err
	}

	// Update cache
	cacheEntry := &CacheFile{
		Channel:     string(cfg.Channel),
		Version:     info.Version,
		BuildNumber: info.BuildNumber,
		ExpiresOn:   time.Now().UTC().Add(cfg.DefaultCheckInterval()).Format(time.RFC3339),
	}

	if err := SaveCache(cacheEntry); err != nil {
		log.Printf("failed to save update cache: %v", err)
	}

	return info, nil
}

func (m *Manager) buildVersionInfoFromCache(cache *CacheFile, channel Channel) (*VersionInfo, error) {
	info := &VersionInfo{
		Version:     cache.Version,
		BuildNumber: cache.BuildNumber,
		Channel:     channel,
	}

	if channel == ChannelDaily {
		// For daily builds, compare cached build number against the running binary's build number.
		// Azure DevOps build IDs are globally monotonically increasing, so a higher build number
		// always means a newer build regardless of the semver prefix.
		currentBuild, currentErr := parseDailyBuildNumber(internal.Version)
		if currentErr == nil && currentBuild > 0 {
			info.HasUpdate = cache.BuildNumber > currentBuild
		} else {
			// Current binary is not a daily build (e.g. stable or dev).
			// Fall back to semver comparison to avoid suggesting a downgrade
			// (e.g. stable 1.23.5 should not "update" to daily 1.5.0).
			dailyVersion, parseErr := semver.Parse(cache.Version)
			currentVersion := internal.VersionInfo().Version
			if parseErr == nil {
				info.HasUpdate = dailyVersion.GT(currentVersion)
			} else {
				info.HasUpdate = true
			}
		}
	} else {
		latestVersion, err := semver.Parse(cache.Version)
		if err != nil {
			return nil, fmt.Errorf("failed to parse cached version: %w", err)
		}
		currentVersion := internal.VersionInfo().Version
		info.HasUpdate = latestVersion.GT(currentVersion)
	}

	return info, nil
}

func (m *Manager) checkStableVersion(ctx context.Context) (*VersionInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stableVersionURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", internal.UserAgent())

	//nolint:gosec // URL is constructed from controlled constants, not user input
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest stable version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch latest stable version, status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	versionText := strings.TrimSpace(string(body))
	latestVersion, err := semver.Parse(versionText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse version %q: %w", versionText, err)
	}

	currentVersion := internal.VersionInfo().Version
	return &VersionInfo{
		Version:   versionText,
		Channel:   ChannelStable,
		HasUpdate: latestVersion.GT(currentVersion),
	}, nil
}

func (m *Manager) checkDailyVersion(ctx context.Context) (*VersionInfo, error) {
	versionURL := blobBaseURL + "/daily/version.txt"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", internal.UserAgent())

	//nolint:gosec // URL is constructed from controlled constants, not user input
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch daily version info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch daily version info, status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	version := strings.TrimSpace(string(body))
	buildNumber, err := parseDailyBuildNumber(version)
	if err != nil {
		return nil, fmt.Errorf("failed to parse daily version %q: %w", version, err)
	}

	// Compare build numbers to determine if an update is available.
	// Azure DevOps build IDs are globally monotonically increasing, so a higher build number
	// always means a newer build regardless of the semver prefix (e.g. daily.5935787 > daily.5935780).
	// Extract current build number from the running binary's version string.
	currentBuild, currentErr := parseDailyBuildNumber(internal.Version)
	hasUpdate := true
	if currentErr == nil && currentBuild > 0 {
		hasUpdate = buildNumber > currentBuild
	} else {
		// Current binary is not a daily build (e.g. stable or dev).
		// Fall back to semver comparison to avoid suggesting a downgrade
		// (e.g. stable 1.23.5 should not "update" to daily 1.5.0).
		dailyVersion, parseErr := semver.Parse(version)
		currentVersion := internal.VersionInfo().Version
		if parseErr == nil {
			hasUpdate = dailyVersion.GT(currentVersion)
		}
	}

	return &VersionInfo{
		Version:     version,
		BuildNumber: buildNumber,
		Channel:     ChannelDaily,
		HasUpdate:   hasUpdate,
	}, nil
}

// ParseDailyBuildNumber extracts the build number from a daily version string.
// e.g. "1.24.0-beta.1-daily.5935787" → 5935787
// Also handles internal.Version format: "1.24.0-beta.1-daily.5935787 (commit ...)" → 5935787
func ParseDailyBuildNumber(version string) (int, error) {
	return parseDailyBuildNumber(version)
}

func parseDailyBuildNumber(version string) (int, error) {
	const prefix = "daily."
	idx := strings.LastIndex(version, prefix)
	if idx == -1 {
		return 0, fmt.Errorf("version %q does not contain %q suffix", version, prefix)
	}

	numStr := version[idx+len(prefix):]
	// Trim anything after the build number (e.g. " (commit ...)" from internal.Version)
	if spaceIdx := strings.IndexByte(numStr, ' '); spaceIdx != -1 {
		numStr = numStr[:spaceIdx]
	}

	buildNumber, err := strconv.Atoi(numStr)
	if err != nil {
		return 0, fmt.Errorf("invalid build number %q in version %q: %w", numStr, version, err)
	}

	return buildNumber, nil
}

// Update performs the update based on the install method.
func (m *Manager) Update(ctx context.Context, cfg *UpdateConfig, writer io.Writer) error {
	installedBy := installer.InstalledBy()

	switch installedBy {
	case installer.InstallTypeBrew:
		return m.updateViaBrew(ctx, cfg, writer)
	case installer.InstallTypeWinget:
		return m.updateViaPackageManager(ctx, "winget", []string{"upgrade", "Microsoft.Azd"}, writer)
	case installer.InstallTypeChoco:
		return m.updateViaPackageManager(ctx, "choco", []string{"upgrade", "azd"}, writer)
	case installer.InstallTypeSh:
		if runtime.GOOS == "windows" {
			return m.updateViaMSI(ctx, cfg, writer)
		}
		return m.updateViaInstallScript(ctx, cfg, writer)
	case installer.InstallTypePs, installer.InstallTypeDeb,
		installer.InstallTypeRpm, installer.InstallTypeUnknown:
		if runtime.GOOS == "windows" {
			return m.updateViaMSI(ctx, cfg, writer)
		}
		return m.updateViaBinaryDownload(ctx, cfg, writer)
	default:
		if runtime.GOOS == "windows" {
			return m.updateViaMSI(ctx, cfg, writer)
		}
		return m.updateViaBinaryDownload(ctx, cfg, writer)
	}
}

func (m *Manager) updateViaBrew(ctx context.Context, cfg *UpdateConfig, writer io.Writer) error {
	fmt.Fprintf(writer, "Checking Homebrew cask installation...\n")

	// Determine which cask is currently installed by checking `brew list --cask`.
	listArgs := exec.NewRunArgs("brew", "list", "--cask")
	listResult, err := m.commandRunner.Run(ctx, listArgs)
	if err != nil {
		log.Printf("brew list --cask failed: %v", err)
	}

	caskOutput := ""
	if err == nil {
		caskOutput = listResult.Stdout
	}

	hasAzd := false
	hasAzdDaily := false
	for line := range strings.SplitSeq(caskOutput, "\n") {
		name := strings.TrimSpace(line)
		if name == "azd@daily" {
			hasAzdDaily = true
		} else if name == "azd" {
			hasAzd = true
		}
	}

	targetChannel := cfg.Channel

	if !hasAzd && !hasAzdDaily {
		// azd is not installed as a cask (formula install or other).
		// Uninstall the non-cask version and install the correct cask.
		fmt.Fprintf(writer, "azd is not installed as a Homebrew cask. Reinstalling as cask...\n")
		if err := m.updateViaPackageManager(ctx, "brew", []string{"uninstall", "azd"}, writer); err != nil {
			log.Printf("brew uninstall azd failed: %v", err)
		}
		switch targetChannel {
		case ChannelStable:
			return m.updateViaPackageManager(ctx, "brew", []string{"install", "--cask", "azure/azd/azd"}, writer)
		case ChannelDaily:
			return m.updateViaPackageManager(ctx, "brew", []string{"install", "--cask", "azure/azd/azd@daily"}, writer)
		default:
			return fmt.Errorf("unsupported channel: %s", targetChannel)
		}
	}

	// Determine if the user is switching channels.
	currentlyDaily := hasAzdDaily
	currentlyStable := hasAzd

	if currentlyDaily && targetChannel == ChannelStable {
		// Switching from daily to stable
		fmt.Fprintf(writer, "Switching from daily to stable channel...\n")
		if err := m.updateViaPackageManager(ctx, "brew", []string{"uninstall", "--cask", "azd@daily"}, writer); err != nil {
			return err
		}
		return m.updateViaPackageManager(ctx, "brew", []string{"install", "--cask", "azure/azd/azd"}, writer)
	}

	if currentlyStable && targetChannel == ChannelDaily {
		// Switching from stable to daily
		fmt.Fprintf(writer, "Switching from stable to daily channel...\n")
		if err := m.updateViaPackageManager(ctx, "brew", []string{"uninstall", "--cask", "azd"}, writer); err != nil {
			return err
		}
		return m.updateViaPackageManager(ctx, "brew", []string{"install", "--cask", "azure/azd/azd@daily"}, writer)
	}

	// Same channel — update in place.
	switch targetChannel {
	case ChannelStable:
		fmt.Fprintf(writer, "Updating azd (stable channel)...\n")
		return m.updateViaPackageManager(ctx, "brew", []string{"upgrade", "--cask", "azure/azd/azd"}, writer)
	case ChannelDaily:
		fmt.Fprintf(writer, "Updating azd (daily channel)...\n")
		return m.updateViaPackageManager(ctx, "brew", []string{"upgrade", "--cask", "azure/azd/azd@daily"}, writer)
	default:
		return fmt.Errorf("unsupported channel: %s", targetChannel)
	}
}

func (m *Manager) updateViaInstallScript(ctx context.Context, cfg *UpdateConfig, writer io.Writer) error {
	fmt.Fprintf(writer, "Updating azd via install script...\n")

	currentPath, err := currentExePath()
	if err != nil {
		return newUpdateError(CodeReplaceFailed, fmt.Errorf("failed to determine current path: %w", err))
	}
	installFolder := filepath.Dir(currentPath)

	// Download install-azd.sh into a uniquely-created temp directory with restrictive permissions
	scriptDir, err := os.MkdirTemp("", "azd-update-*")
	if err != nil {
		return newUpdateError(CodeDownloadFailed, fmt.Errorf("failed to create temp directory: %w", err))
	}
	defer os.RemoveAll(scriptDir)

	// Restrict the temp directory so only the current user can read/write/traverse it.
	if err := os.Chmod(scriptDir, 0o700); err != nil {
		return newUpdateError(CodeDownloadFailed, fmt.Errorf("failed to set temp directory permissions: %w", err))
	}

	scriptPath := filepath.Join(scriptDir, "install-azd.sh")

	if err := m.downloadFile(ctx, installShScriptURL, scriptPath, writer); err != nil {
		return newUpdateError(CodeDownloadFailed, fmt.Errorf("failed to download install script: %w", err))
	}

	// Make the script executable
	if err := os.Chmod(scriptPath, 0o500); err != nil {
		return newUpdateError(CodeReplaceFailed, fmt.Errorf("failed to set script permissions: %w", err))
	}

	versionArg := string(cfg.Channel)
	runArgs := exec.NewRunArgs("bash", scriptPath,
		"--version", versionArg,
		"--install-folder", installFolder,
		"--symlink-folder", "",
	)
	runArgs = runArgs.WithStdOut(writer).WithStdErr(writer).WithInteractive(true)

	log.Printf("Running install script: bash %s --version %s --install-folder %s --symlink-folder \"\"",
		scriptPath, versionArg, installFolder)
	fmt.Fprintf(writer, "Installing azd %s channel to %s...\n", cfg.Channel, installFolder)

	result, err := m.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return newUpdateError(CodeReplaceFailed, fmt.Errorf("install script failed: %w", err))
	}

	if result.ExitCode != 0 {
		return newUpdateErrorf(CodeReplaceFailed,
			"install script failed with exit code %d", result.ExitCode)
	}

	log.Printf("Install script completed successfully")
	return nil
}

func (m *Manager) updateViaPackageManager(
	ctx context.Context,
	command string,
	args []string,
	writer io.Writer,
) error {
	fmt.Fprintf(writer, "Updating azd via %s...\n", command)

	runArgs := exec.NewRunArgs(command, args...)
	runArgs = runArgs.WithStdOut(writer).WithStdErr(writer).WithInteractive(true)

	result, err := m.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return newUpdateError(CodePackageManagerFailed, err)
	}

	if result.ExitCode != 0 {
		return newUpdateErrorf(CodePackageManagerFailed,
			"package manager update failed with exit code %d", result.ExitCode)
	}

	return nil
}

func (m *Manager) updateViaMSI(ctx context.Context, cfg *UpdateConfig, writer io.Writer) error {
	// Verify the install is the standard per-user MSI configuration.
	// install-azd.ps1 installs with ALLUSERS=2 to %LOCALAPPDATA%\Programs\Azure Dev CLI.
	// If the current install is non-standard, abort and advise the user.
	if err := isStandardMSIInstall(); err != nil {
		return err
	}

	//  1. Rename the running exe to temp (frees the path; process continues via the OS handle)
	//  2. Copy it back as an unlocked safety net (if killed at any point, azd.exe still exists)
	//  3. The MSI will overwrite the unlocked safety copy with the new version
	fmt.Fprintf(writer, "Backing up current azd executable...\n")
	originalPath, backupPath, err := backupCurrentExe()
	if err != nil {
		return newUpdateError(CodeReplaceFailed, fmt.Errorf("failed to backup current executable: %w", err))
	}

	// Track whether the install succeeded so we know whether to restore or clean up.
	updateSucceeded := false
	defer func() {
		if updateSucceeded {
			// Remove the temp backup directory. If this fails, the OS
			// will clean it up eventually since it lives under %TEMP%.
			_ = os.RemoveAll(filepath.Dir(backupPath))
			return
		}
		// Update failed — restore the backup so the user has the original binary.
		fmt.Fprintf(writer, "Restoring previous version...\n")
		if restoreErr := restoreExeFromBackup(originalPath, backupPath); restoreErr != nil {
			fmt.Fprintf(writer, "WARNING: failed to restore previous version: %v\n", restoreErr)
			fmt.Fprintf(writer, "Your backup is at: %s\n", backupPath)
			fmt.Fprintf(writer, "To recover manually, copy it to: %s\n", originalPath)
		}
	}()

	// Run the install script synchronously. The MSI overwrites the unlocked
	// safety copy at the original path with the new version.
	psArgs := buildInstallScriptArgs(cfg.Channel)

	// Snapshot the safety copy's mod time before the install so we can detect
	// whether the MSI actually replaced the file. A plain os.Stat after install
	// would always succeed because the safety copy already exists at originalPath.
	preInfo, statErr := os.Stat(originalPath)
	if statErr != nil {
		return newUpdateError(CodeReplaceFailed,
			fmt.Errorf("failed to stat safety copy before install: %w", statErr))
	}

	log.Printf("Running install script: powershell %s", strings.Join(psArgs, " "))
	fmt.Fprintf(writer, "Installing azd %s channel...\n", cfg.Channel)

	runArgs := exec.NewRunArgs("powershell", psArgs...).
		WithStdOut(writer).
		WithStdErr(writer)

	if _, err := m.commandRunner.Run(ctx, runArgs); err != nil {
		return newUpdateError(CodeReplaceFailed, fmt.Errorf("install script failed: %w", err))
	}

	// Verify the MSI actually replaced the binary by comparing mod time and
	// size against the pre-install safety copy. If both are identical the MSI
	// did not write a new file (silent failure).
	postInfo, statErr := os.Stat(originalPath)
	if statErr != nil {
		return newUpdateError(CodeReplaceFailed,
			fmt.Errorf("install script completed but %s was not found", originalPath))
	}

	if postInfo.ModTime().Equal(preInfo.ModTime()) && postInfo.Size() == preInfo.Size() {
		return newUpdateError(CodeReplaceFailed,
			fmt.Errorf("install script completed but the binary at %s was not updated "+
				"(file unchanged); the MSI may have failed silently", originalPath))
	}

	updateSucceeded = true
	log.Printf("Update completed successfully")
	return nil
}

func (m *Manager) updateViaBinaryDownload(ctx context.Context, cfg *UpdateConfig, writer io.Writer) error {
	downloadURL, err := m.buildDownloadURL(cfg.Channel)
	if err != nil {
		return err
	}

	fmt.Fprintf(writer, "Downloading azd from %s...\n", downloadURL)

	// Download to a temp file
	tempDir := os.TempDir()
	archiveName := fmt.Sprintf("azd-%s-%s%s", runtime.GOOS, runtime.GOARCH, archiveExtension())
	tempArchivePath := filepath.Join(tempDir, archiveName)

	if err := m.downloadFile(ctx, downloadURL, tempArchivePath, writer); err != nil {
		return newUpdateError(CodeDownloadFailed, err)
	}
	defer os.Remove(tempArchivePath)

	// Extract the binary
	binaryName := "azd"
	if runtime.GOOS == "windows" {
		binaryName = "azd.exe"
	}

	tempBinaryPath := filepath.Join(tempDir, "azd-update-"+binaryName)
	if err := extractBinary(tempArchivePath, binaryName, tempBinaryPath); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	defer os.Remove(tempBinaryPath)

	// Make executable on unix
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tempBinaryPath, 0o755); err != nil {
			return fmt.Errorf("failed to set permissions: %w", err)
		}
	}

	// Verify code signature (macOS and Windows only)
	if err := m.verifyCodeSignature(ctx, tempBinaryPath, writer); err != nil {
		return newUpdateError(CodeSignatureInvalid, err)
	}

	// Determine current binary location
	currentBinaryPath, err := currentExePath()
	if err != nil {
		return fmt.Errorf("failed to determine current binary path: %w", err)
	}

	// Replace the binary (may need elevation)
	fmt.Fprintf(writer, "Installing update...\n")
	if err := m.replaceBinary(ctx, tempBinaryPath, currentBinaryPath); err != nil {
		return newUpdateError(CodeReplaceFailed, err)
	}

	return nil
}

func (m *Manager) buildDownloadURL(channel Channel) (string, error) {
	platform := runtime.GOOS
	arch := runtime.GOARCH
	ext := archiveExtension()

	var folder string
	switch channel {
	case ChannelStable:
		folder = "stable"
	case ChannelDaily:
		folder = "daily"
	default:
		return "", fmt.Errorf("unsupported channel: %s", channel)
	}

	return fmt.Sprintf("%s/%s/azd-%s-%s%s", blobBaseURL, folder, platform, arch, ext), nil
}

func archiveExtension() string {
	if runtime.GOOS == "linux" {
		return ".tar.gz"
	}
	return ".zip"
}

func (m *Manager) downloadFile(ctx context.Context, url string, destPath string, writer io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", internal.UserAgent())

	//nolint:gosec // URL is constructed from controlled constants, not user input
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Show progress
	contentLength := resp.ContentLength
	var src io.Reader = resp.Body
	if contentLength > 0 {
		src = &progressReader{
			reader: resp.Body,
			total:  contentLength,
			writer: writer,
		}
	}

	_, err = io.Copy(out, src)
	if contentLength > 0 {
		fmt.Fprintln(writer) // newline after progress
	}

	return err
}

// verifyCodeSignature checks the code signature of the downloaded binary.
// On macOS, it uses codesign -v. On Windows, it uses Get-AuthenticodeSignature.
// On Linux or if the command runner is nil, verification is skipped gracefully.
func (m *Manager) verifyCodeSignature(ctx context.Context, binaryPath string, writer io.Writer) error {
	if m.commandRunner == nil {
		log.Printf("no command runner available, skipping code signature verification")
		return nil
	}

	switch runtime.GOOS {
	case "darwin":
		return m.verifyCodesignMac(ctx, binaryPath, writer)
	case "windows":
		return m.verifyAuthenticode(ctx, binaryPath, writer)
	default:
		// Linux has no standard code signing verification tool
		log.Printf("code signing verification not available on %s, skipping", runtime.GOOS)
		return nil
	}
}

func (m *Manager) verifyCodesignMac(ctx context.Context, binaryPath string, writer io.Writer) error {
	runArgs := exec.NewRunArgs("codesign", "-v", "--strict", binaryPath)
	result, err := m.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return newUpdateError(CodeSignatureInvalid, fmt.Errorf("codesign verification failed: %w", err))
	}

	if result.ExitCode != 0 {
		return newUpdateError(CodeSignatureInvalid, fmt.Errorf(
			"code signature verification failed for %s (exit code %d): %s",
			binaryPath, result.ExitCode, result.Stderr,
		))
	}

	fmt.Fprintf(writer, "Code signature verified.\n")
	return nil
}

func (m *Manager) verifyAuthenticode(ctx context.Context, binaryPath string, writer io.Writer) error {
	// PowerShell script to check Authenticode signature status
	script := fmt.Sprintf(
		`$sig = Get-AuthenticodeSignature -FilePath '%s'; if ($sig.Status -ne 'Valid') { `+
			`Write-Error "Signature status: $($sig.Status)"; exit 1 }`,
		binaryPath,
	)

	runArgs := exec.NewRunArgs("powershell", "-NoProfile", "-Command", script)
	result, err := m.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return newUpdateError(CodeSignatureInvalid, fmt.Errorf("Authenticode verification failed: %w", err))
	}

	if result.ExitCode != 0 {
		return newUpdateError(CodeSignatureInvalid, fmt.Errorf(
			"Authenticode signature verification failed for %s: %s",
			binaryPath, result.Stderr,
		))
	}

	fmt.Fprintf(writer, "Code signature verified.\n")
	return nil
}

func (m *Manager) replaceBinary(ctx context.Context, newBinaryPath, currentBinaryPath string) error {
	// Try direct replacement first
	err := os.Rename(newBinaryPath, currentBinaryPath)
	if err == nil {
		return nil
	}

	// If direct rename fails (cross-device or permissions), try copy
	err = copyFile(newBinaryPath, currentBinaryPath)
	if err == nil {
		return nil
	}

	// On unix, try with sudo if permission denied
	if runtime.GOOS != "windows" {
		log.Printf("direct replacement failed (%v), trying with sudo", err)
		// Remove the destination first to avoid "Text file busy" (ETXTBSY) errors.
		rmArgs := exec.NewRunArgs("sudo", "rm", "-f", currentBinaryPath)
		rmArgs = rmArgs.WithInteractive(true)
		rmResult, rmErr := m.commandRunner.Run(ctx, rmArgs)
		if rmErr != nil {
			return newUpdateError(CodeElevationFailed, fmt.Errorf("sudo rm failed: %w", rmErr))
		}
		if rmResult.ExitCode != 0 {
			return newUpdateErrorf(CodeElevationFailed,
				"sudo rm failed with exit code %d", rmResult.ExitCode)
		}

		cpArgs := exec.NewRunArgs("sudo", "cp", newBinaryPath, currentBinaryPath)
		cpArgs = cpArgs.WithInteractive(true)
		cpResult, cpErr := m.commandRunner.Run(ctx, cpArgs)
		if cpErr != nil {
			return newUpdateError(CodeElevationFailed, fmt.Errorf("sudo cp failed: %w", cpErr))
		}
		if cpResult.ExitCode != 0 {
			return newUpdateErrorf(CodeElevationFailed,
				"sudo cp failed with exit code %d", cpResult.ExitCode)
		}
		return nil
	}

	return fmt.Errorf("failed to replace binary: %w", err)
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

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	srcInfo, err := in.Stat()
	if err != nil {
		return err
	}

	// On unix, always remove the destination file before creating a new one.
	// Truncating a running binary in place (os.Create on existing file) corrupts
	// memory-mapped code pages and causes macOS to SIGKILL the process.
	// Removing first creates a new inode — the OS keeps the old inode alive
	// for the running process via its fd.
	if runtime.GOOS != "windows" {
		removeErr := os.Remove(dst)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("removing %s before replacement: %w", dst, removeErr)
		}
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}

	if err := out.Sync(); err != nil {
		return err
	}

	// Preserve source file permissions. After remove-then-create, the new file gets
	// default 0666 permissions instead of the original executable permissions.
	//nolint:gosec // path is from the source file stat, not user input
	return os.Chmod(dst, srcInfo.Mode().Perm())
}

// extractBinary extracts the azd binary from the archive to destPath.
// platformBinaryName returns the expected platform-specific binary name in archives (e.g., "azd-darwin-amd64").
func platformBinaryName() string {
	name := fmt.Sprintf("azd-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func extractBinary(archivePath, binaryName, destPath string) error {
	if strings.HasSuffix(archivePath, ".tar.gz") {
		return extractFromTarGz(archivePath, binaryName, destPath)
	}
	return extractFromZip(archivePath, binaryName, destPath)
}

func isAzdBinary(name, binaryName string) bool {
	return name == binaryName || name == platformBinaryName()
}

func extractFromTarGz(archivePath, binaryName, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		name := filepath.Base(header.Name)
		if isAzdBinary(name, binaryName) {
			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer out.Close()

			//nolint:gosec
			_, err = io.Copy(out, tr)
			return err
		}
	}

	return fmt.Errorf("binary %q not found in archive", binaryName)
}

func extractFromZip(archivePath, binaryName, destPath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if isAzdBinary(name, binaryName) {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer out.Close()

			//nolint:gosec
			_, err = io.Copy(out, rc)
			return err
		}
	}

	return fmt.Errorf("binary %q not found in archive", binaryName)
}

// IsPackageManagerInstall returns true if azd was installed via a package manager.
func IsPackageManagerInstall() bool {
	switch installer.InstalledBy() {
	case installer.InstallTypeWinget, installer.InstallTypeChoco:
		return true
	default:
		return false
	}
}

// PackageManagerUninstallCmd returns the uninstall command for the detected package manager.
func PackageManagerUninstallCmd(installedBy installer.InstallType) string {
	switch installedBy {
	case installer.InstallTypeBrew:
		return "brew uninstall azd"
	case installer.InstallTypeWinget:
		return "winget uninstall Microsoft.Azd"
	case installer.InstallTypeChoco:
		return "choco uninstall azd"
	default:
		return "your package manager"
	}
}

// progressReader wraps an io.Reader to report download progress.
type progressReader struct {
	reader  io.Reader
	total   int64
	current int64
	writer  io.Writer
	lastPct int
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.current += int64(n)
	pct := int(float64(pr.current) / float64(pr.total) * 100)
	if pct != pr.lastPct && pct%10 == 0 {
		fmt.Fprintf(pr.writer, "\rDownloading... %d%%", pct)
		pr.lastPct = pct
	}
	return n, err
}

const stagingDirName = "staging"

// stagingDir returns the path to ~/.azd/staging/.
func stagingDir() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, stagingDirName), nil
}

// StagedBinaryPath returns the path where a staged binary would be placed.
func StagedBinaryPath() (string, error) {
	dir, err := stagingDir()
	if err != nil {
		return "", err
	}

	binaryName := "azd"
	if runtime.GOOS == "windows" {
		binaryName = "azd.exe"
	}

	return filepath.Join(dir, binaryName), nil
}

// HasStagedUpdate returns true if a staged binary exists and is ready to apply.
func HasStagedUpdate() bool {
	path, err := StagedBinaryPath()
	if err != nil {
		return false
	}

	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// StageUpdate downloads the latest binary to ~/.azd/staging/ for later apply.
// This is intended to run in the background without user interaction.
func (m *Manager) StageUpdate(ctx context.Context, cfg *UpdateConfig) error {
	// Only stage for direct binary installs, not package managers
	if IsPackageManagerInstall() {
		log.Printf("auto-update: package manager install, skipping staging")
		return nil
	}

	// On Windows, updates are applied via MSI (updateViaMSI); staging a standalone binary
	// would be unused and potentially inconsistent with the MSI-based install.
	if runtime.GOOS == "windows" {
		log.Printf("auto-update: windows MSI-based install, skipping staging")
		return nil
	}

	downloadURL, err := m.buildDownloadURL(cfg.Channel)
	if err != nil {
		return err
	}

	dir, err := stagingDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create staging directory: %w", err)
	}

	// Download archive to staging dir
	archiveName := fmt.Sprintf("azd-%s-%s%s", runtime.GOOS, runtime.GOARCH, archiveExtension())
	archivePath := filepath.Join(dir, archiveName)

	if err := m.downloadFile(ctx, downloadURL, archivePath, io.Discard); err != nil {
		return fmt.Errorf("auto-update download failed: %w", err)
	}
	defer os.Remove(archivePath)

	// Extract binary to staging dir
	binaryName := "azd"
	if runtime.GOOS == "windows" {
		binaryName = "azd.exe"
	}

	stagedPath, err := StagedBinaryPath()
	if err != nil {
		return err
	}

	if err := extractBinary(archivePath, binaryName, stagedPath); err != nil {
		return fmt.Errorf("auto-update extraction failed: %w", err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(stagedPath, 0o755); err != nil {
			return fmt.Errorf("failed to set permissions on staged binary: %w", err)
		}
	}

	log.Printf("auto-update: staged new binary to %s", stagedPath)
	return nil
}

// CleanStagedUpdate removes any staged binary, e.g. when auto-update is disabled after staging.
func CleanStagedUpdate() {
	path, err := StagedBinaryPath()
	if err != nil {
		return
	}

	if _, err := os.Stat(path); err == nil {
		os.Remove(path)
		dir, _ := stagingDir()
		os.Remove(dir)
		log.Printf("auto-update: cleaned up staged binary at %s", path)
	}
}

// ErrNeedsElevation is returned when the staged update can't be applied without elevation.
var ErrNeedsElevation = fmt.Errorf("applying staged update requires elevation")

// ApplyStagedUpdate replaces the current binary with the staged one and cleans up.
// Returns the path to the new binary if applied, or empty string if no staged update exists.
// Returns ErrNeedsElevation if the install location is not writable (e.g. /opt/microsoft/azd/).
func ApplyStagedUpdate() (string, error) {
	stagedPath, err := StagedBinaryPath()
	if err != nil {
		return "", err
	}

	if !HasStagedUpdate() {
		return "", nil
	}

	// Verify the staged binary is valid before applying.
	// A background goroutine may have been interrupted mid-download, leaving a truncated file.
	if err := verifyStagedBinary(stagedPath); err != nil {
		log.Printf("auto-update: staged binary is invalid, cleaning up: %v", err)
		os.Remove(stagedPath)
		dir, _ := stagingDir()
		os.Remove(dir)
		return "", nil
	}

	currentPath, err := currentExePath()
	if err != nil {
		return "", fmt.Errorf("failed to determine current binary: %w", err)
	}

	// Check if we can write to the install location without elevation
	if err := copyFile(stagedPath, currentPath); err != nil {
		if os.IsPermission(err) {
			// Keep the staged binary — user can apply via 'azd update'
			log.Printf("auto-update: install location %s requires elevation, skipping apply", currentPath)
			return "", ErrNeedsElevation
		}

		// Non-permission error — clean up to avoid retrying a broken stage
		os.Remove(stagedPath)
		return "", fmt.Errorf("failed to apply staged update: %w", err)
	}

	// Clean up staging directory
	os.Remove(stagedPath)
	dir, _ := stagingDir()
	os.Remove(dir) // remove dir if empty

	log.Printf("auto-update: applied staged binary from %s to %s", stagedPath, currentPath)
	return currentPath, nil
}

// verifyStagedBinary performs basic validation on the staged binary.
// Checks minimum file size (catches truncated downloads and non-binary files).
// On macOS, also runs codesign verification. Unsigned binaries (e.g. dev builds) are allowed,
// but binaries with invalid/corrupted signatures are rejected.
func verifyStagedBinary(path string) error {
	// Size sanity check — azd binary is typically 40-65 MB.
	// A minimum of 1 MB catches truncated downloads and non-binary files
	// that codesign would incorrectly report as "not signed at all".
	const minBinarySize = 1024 * 1024 // 1 MB
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("cannot stat staged binary: %w", err)
	}
	if info.Size() < minBinarySize {
		return fmt.Errorf("staged binary too small (%d bytes), likely corrupted", info.Size())
	}

	if runtime.GOOS == "darwin" {
		//nolint:gosec // path is not user-controlled — it's the well-known staging directory
		cmd := osexec.Command("codesign", "-v", "--strict", path)
		if combinedOut, err := cmd.CombinedOutput(); err != nil {
			outStr := string(combinedOut)
			// "not signed at all" is OK — dev builds and some installs are unsigned.
			// Only reject binaries with invalid/corrupted signatures (e.g. truncated downloads).
			if strings.Contains(outStr, "not signed") {
				log.Printf("auto-update: staged binary is unsigned, allowing: %s", outStr)
				return nil
			}
			return fmt.Errorf("code signature invalid: %s", outStr)
		}
	}

	return nil
}

const appliedMarkerFile = "update-applied.txt"

// WriteAppliedMarker writes a marker file recording the version before auto-update.
// This is read on the next startup to display an "updated" banner.
func WriteAppliedMarker(fromVersion string) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return
	}

	path := filepath.Join(configDir, appliedMarkerFile)
	_ = os.WriteFile(path, []byte(fromVersion), osutil.PermissionFile)
}

// ReadAppliedMarker reads the applied update marker and returns the previous version.
func ReadAppliedMarker() (string, error) {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(filepath.Join(configDir, appliedMarkerFile))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}

// RemoveAppliedMarker deletes the applied update marker file.
func RemoveAppliedMarker() {
	configDir, err := config.GetUserConfigDir()
	if err != nil {
		return
	}

	os.Remove(filepath.Join(configDir, appliedMarkerFile))
}
