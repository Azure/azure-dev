// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

func TestExpectedPerUserInstallDir(t *testing.T) {
	tests := []struct {
		name         string
		localAppData string
		want         string
	}{
		{
			name:         "standard",
			localAppData: `C:\Users\testuser\AppData\Local`,
			want:         `C:\Users\testuser\AppData\Local\Programs\Azure Dev CLI`,
		},
		{
			name:         "empty LOCALAPPDATA",
			localAppData: "",
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOCALAPPDATA", tt.localAppData)
			got := expectedPerUserInstallDir()
			require.Equal(t, tt.want, got)
		})
	}
}

func TestVersionFlag(t *testing.T) {
	tests := []struct {
		name    string
		channel Channel
		want    string
	}{
		{"stable channel", ChannelStable, "stable"},
		{"daily channel", ChannelDaily, "daily"},
		{"unknown defaults to stable", Channel("nightly"), "stable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := versionFlag(tt.channel)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBuildInstallScriptArgs(t *testing.T) {
	tests := []struct {
		name    string
		channel Channel
		// We check that certain substrings appear in the constructed args
		wantContains []string
	}{
		{
			name:    "stable",
			channel: ChannelStable,
			wantContains: []string{
				"-NoProfile",
				"-ExecutionPolicy", "Bypass",
				"-Command",
				installScriptURL,
				"-Version 'stable'",
				"-SkipVerify",
			},
		},
		{
			name:    "daily",
			channel: ChannelDaily,
			wantContains: []string{
				"-NoProfile",
				"-ExecutionPolicy", "Bypass",
				"-Command",
				installScriptURL,
				"-Version 'daily'",
				"-SkipVerify",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildInstallScriptArgs(tt.channel)
			require.NotNil(t, args)
			require.True(t, len(args) > 0, "expected non-empty args slice")

			// Join all args to make substring searches easier
			joined := strings.Join(args, " ")
			for _, s := range tt.wantContains {
				require.Contains(t, joined, s, "expected args to contain %q", s)
			}
		})
	}
}

func TestBuildInstallScriptArgs_Structure(t *testing.T) {
	args := buildInstallScriptArgs(ChannelStable)

	// The args should be: ["-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", <script>]
	require.Equal(t, 5, len(args), "expected exactly 5 args")
	require.Equal(t, "-NoProfile", args[0])
	require.Equal(t, "-ExecutionPolicy", args[1])
	require.Equal(t, "Bypass", args[2])
	require.Equal(t, "-Command", args[3])

	// The script (args[4]) should be a single string containing the full PowerShell pipeline
	script := args[4]
	require.Contains(t, script, "Invoke-RestMethod")
	require.Contains(t, script, installScriptURL)
	require.Contains(t, script, "-SkipVerify")
	require.Contains(t, script, "Remove-Item")
}

func TestCheckOtherAzdProcesses_NoneFound(t *testing.T) {
	mockRunner := mockexec.NewMockCommandRunner()
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "Get-Process -Name azd")
	}).Respond(exec.NewRunResult(0, "", ""))

	err := checkOtherAzdProcesses(context.Background(), mockRunner)
	require.NoError(t, err)
}

func TestCheckOtherAzdProcesses_OtherInstanceFound(t *testing.T) {
	mockRunner := mockexec.NewMockCommandRunner()
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "Get-Process -Name azd")
	}).Respond(exec.NewRunResult(0, "12345\n67890\n", ""))

	err := checkOtherAzdProcesses(context.Background(), mockRunner)
	require.Error(t, err)

	var updateErr *UpdateError
	require.ErrorAs(t, err, &updateErr)
	require.Equal(t, CodeOtherProcessesRunning, updateErr.Code)
	require.Contains(t, err.Error(), "12345")
	require.Contains(t, err.Error(), "67890")
}

func TestCheckOtherAzdProcesses_CommandFails(t *testing.T) {
	// When the PowerShell command itself fails, checkOtherAzdProcesses should log
	// a warning but return nil (allow the update to proceed).
	mockRunner := mockexec.NewMockCommandRunner()
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "Get-Process -Name azd")
	}).SetError(fmt.Errorf("powershell not found"))

	err := checkOtherAzdProcesses(context.Background(), mockRunner)
	require.NoError(t, err, "should not block update when process check fails")
}

func TestCheckOtherAzdProcesses_WhitespaceOnlyOutput(t *testing.T) {
	mockRunner := mockexec.NewMockCommandRunner()
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "Get-Process -Name azd")
	}).Respond(exec.NewRunResult(0, "   \n  \n  ", ""))

	err := checkOtherAzdProcesses(context.Background(), mockRunner)
	require.NoError(t, err, "whitespace-only output should be treated as no other processes")
}

func TestIsStandardMSIInstall_StandardPath(t *testing.T) {
	// Get the actual exe path and set LOCALAPPDATA so that
	// expectedPerUserInstallDir() == filepath.Dir(exePath).
	// expectedPerUserInstallDir = LOCALAPPDATA + \Programs\Azure Dev CLI
	// So we need LOCALAPPDATA = filepath.Dir(exePath) stripped of "\Programs\Azure Dev CLI".
	exePath, err := os.Executable()
	require.NoError(t, err)
	exePath, err = filepath.EvalSymlinks(exePath)
	require.NoError(t, err)

	actualDir := filepath.Dir(exePath)
	suffix := filepath.Join("Programs", "Azure Dev CLI")
	if !strings.HasSuffix(strings.ToLower(filepath.Clean(actualDir)), strings.ToLower(suffix)) {
		// The test binary isn't in the expected suffix path (typical in CI/dev).
		// Skip this test since we can't synthetically set LOCALAPPDATA to match.
		t.Skipf("test binary dir %q does not end with %q; skipping standard-path test", actualDir, suffix)
	}

	localAppData := strings.TrimSuffix(filepath.Clean(actualDir), filepath.Clean(suffix))
	localAppData = strings.TrimRight(localAppData, string(filepath.Separator))
	t.Setenv("LOCALAPPDATA", localAppData)

	err = isStandardMSIInstall()
	require.NoError(t, err)
}

func TestIsStandardMSIInstall_NonStandardPath(t *testing.T) {
	// Set LOCALAPPDATA to a directory that definitely doesn't match the test binary location
	t.Setenv("LOCALAPPDATA", `C:\SomeOtherLocation`)

	err := isStandardMSIInstall()
	require.Error(t, err)

	var updateErr *UpdateError
	require.ErrorAs(t, err, &updateErr)
	require.Equal(t, CodeNonStandardInstall, updateErr.Code)
	require.Contains(t, err.Error(), "non-standard location")
}

func TestIsStandardMSIInstall_MissingLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", "")

	err := isStandardMSIInstall()
	require.Error(t, err)

	var updateErr *UpdateError
	require.ErrorAs(t, err, &updateErr)
	require.Equal(t, CodeNonStandardInstall, updateErr.Code)
	require.Contains(t, err.Error(), "LOCALAPPDATA")
}

func TestBackupCurrentExe(t *testing.T) {
	// Create a temp dir with a fake azd.exe
	dir := t.TempDir()
	exePath := filepath.Join(dir, "azd.exe")
	require.NoError(t, os.WriteFile(exePath, []byte("original"), 0o755))

	// backupCurrentExe relies on os.Executable(), which we can't override,
	// so we test the lower-level rename logic directly.
	backupPath := exePath + ".old.1234567890"
	require.NoError(t, os.Rename(exePath, backupPath))

	// Original should be gone, backup should exist
	_, err := os.Stat(exePath)
	require.True(t, os.IsNotExist(err), "original should no longer exist after rename")

	data, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
}

func TestRestoreExeFromBackup(t *testing.T) {
	dir := t.TempDir()
	originalPath := filepath.Join(dir, "azd.exe")
	backupPath := originalPath + ".old.1234567890"

	// Create the backup file
	require.NoError(t, os.WriteFile(backupPath, []byte("backup-content"), 0o755))

	// Restore should move backup → original
	require.NoError(t, restoreExeFromBackup(originalPath, backupPath))

	data, err := os.ReadFile(originalPath)
	require.NoError(t, err)
	require.Equal(t, "backup-content", string(data))

	_, err = os.Stat(backupPath)
	require.True(t, os.IsNotExist(err), "backup should be gone after restore")
}

func TestRestoreExeFromBackup_RemovesPartialInstall(t *testing.T) {
	dir := t.TempDir()
	originalPath := filepath.Join(dir, "azd.exe")
	backupPath := originalPath + ".old.1234567890"

	// Simulate a partial install that left a corrupt file at the original path
	require.NoError(t, os.WriteFile(originalPath, []byte("partial-new"), 0o755))
	require.NoError(t, os.WriteFile(backupPath, []byte("good-backup"), 0o755))

	require.NoError(t, restoreExeFromBackup(originalPath, backupPath))

	data, err := os.ReadFile(originalPath)
	require.NoError(t, err)
	require.Equal(t, "good-backup", string(data), "should have restored backup content")
}

func TestCleanupOldBackups(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "azd.exe")

	// Create the main exe and several backup files
	require.NoError(t, os.WriteFile(exePath, []byte("current"), 0o755))
	require.NoError(t, os.WriteFile(exePath+".old.111", []byte("old1"), 0o755))
	require.NoError(t, os.WriteFile(exePath+".old.222", []byte("old2"), 0o755))
	require.NoError(t, os.WriteFile(exePath+".old.333", []byte("old3"), 0o755))

	// Also create a file that shouldn't be touched
	otherFile := filepath.Join(dir, "other.txt")
	require.NoError(t, os.WriteFile(otherFile, []byte("keep"), 0o644))

	cleanupOldBackups(exePath)

	// Backups should be removed
	for _, suffix := range []string{".old.111", ".old.222", ".old.333"} {
		_, err := os.Stat(exePath + suffix)
		require.True(t, os.IsNotExist(err), "backup %s should be cleaned up", suffix)
	}

	// The main exe and unrelated file should remain
	_, err := os.Stat(exePath)
	require.NoError(t, err, "main exe should still exist")
	_, err = os.Stat(otherFile)
	require.NoError(t, err, "unrelated file should still exist")
}

func TestUpdateErrorCodes(t *testing.T) {
	// Verify the new error codes are distinct and well-formed
	codes := []string{
		CodeOtherProcessesRunning,
		CodeNonStandardInstall,
	}

	for _, code := range codes {
		require.True(t, strings.HasPrefix(code, "update."), "code %q should have update. prefix", code)
	}

	require.NotEqual(t, CodeOtherProcessesRunning, CodeNonStandardInstall)
}

func TestCheckOtherAzdProcesses_ExcludesCurrentPID(t *testing.T) {
	// Verify the PowerShell script excludes the current PID
	currentPID := os.Getpid()
	pidStr := fmt.Sprintf("%d", currentPID)

	mockRunner := mockexec.NewMockCommandRunner()
	var capturedCommand string
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		if strings.Contains(command, "Get-Process -Name azd") {
			capturedCommand = command
			return true
		}
		return false
	}).Respond(exec.NewRunResult(0, "", ""))

	_ = checkOtherAzdProcesses(context.Background(), mockRunner)
	require.Contains(t, capturedCommand, pidStr,
		"PowerShell command should reference current PID %s to exclude it", pidStr)
}

func TestCheckOtherAzdProcesses_ParsesMultiplePIDs(t *testing.T) {
	mockRunner := mockexec.NewMockCommandRunner()
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "Get-Process -Name azd")
	}).Respond(exec.NewRunResult(0, "1111\n2222\n3333\n", ""))

	err := checkOtherAzdProcesses(context.Background(), mockRunner)
	require.Error(t, err)

	var updateErr *UpdateError
	require.ErrorAs(t, err, &updateErr)
	require.Contains(t, err.Error(), "3 other azd process(es)")

	// Verify all PIDs are mentioned
	errMsg := err.Error()
	require.True(t, strings.Contains(errMsg, "1111") &&
		strings.Contains(errMsg, "2222") &&
		strings.Contains(errMsg, "3333"),
		"error message should include all PIDs")
}

func TestInstallScriptURL(t *testing.T) {
	require.Equal(t, "https://aka.ms/install-azd.ps1", installScriptURL)
}

// TestUpdateViaMSI_OtherProcessesBlocks verifies that updateViaMSI returns an error
// when other azd processes are detected.
func TestUpdateViaMSI_OtherProcessesBlocks(t *testing.T) {
	mockRunner := mockexec.NewMockCommandRunner()
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "Get-Process -Name azd")
	}).Respond(exec.NewRunResult(0, "99999\n", ""))

	m := NewManager(mockRunner, nil)
	var buf strings.Builder
	cfg := &UpdateConfig{Channel: ChannelStable}

	err := m.updateViaMSI(context.Background(), cfg, &buf)
	require.Error(t, err)

	var updateErr *UpdateError
	require.True(t, errors.As(err, &updateErr))
	require.Equal(t, CodeOtherProcessesRunning, updateErr.Code)
}

// TestUpdateViaMSI_NonStandardInstallBlocks verifies that updateViaMSI returns an error
// when the install location doesn't match the expected per-user path.
func TestUpdateViaMSI_NonStandardInstallBlocks(t *testing.T) {
	// Mock: no other processes found
	mockRunner := mockexec.NewMockCommandRunner()
	mockRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "Get-Process -Name azd")
	}).Respond(exec.NewRunResult(0, "", ""))

	// Set LOCALAPPDATA to something that won't match the test binary location
	t.Setenv("LOCALAPPDATA", `C:\NonExistentPath`)

	m := NewManager(mockRunner, nil)
	var buf strings.Builder
	cfg := &UpdateConfig{Channel: ChannelStable}

	err := m.updateViaMSI(context.Background(), cfg, &buf)
	require.Error(t, err)

	var updateErr *UpdateError
	require.True(t, errors.As(err, &updateErr))
	require.Equal(t, CodeNonStandardInstall, updateErr.Code)
}
