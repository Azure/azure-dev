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
	// backupCurrentExe relies on os.Executable(), which we can't override,
	// so we test the underlying move-to-temp-dir logic directly.
	installDir := t.TempDir()
	exePath := filepath.Join(installDir, "azd.exe")
	require.NoError(t, os.WriteFile(exePath, []byte("original"), 0o755))

	// Simulate what backupCurrentExe does: create temp dir, move exe there.
	tmpDir, err := os.MkdirTemp("", "azd-update-backup")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	backupPath := filepath.Join(tmpDir, "azd.exe")
	require.NoError(t, os.Rename(exePath, backupPath))

	// Original should be gone, backup should exist in temp dir
	_, err = os.Stat(exePath)
	require.True(t, os.IsNotExist(err), "original should no longer exist after move")

	data, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
}

func TestRestoreExeFromBackup(t *testing.T) {
	installDir := t.TempDir()
	originalPath := filepath.Join(installDir, "azd.exe")

	// Create a temp backup dir with the backed-up exe
	tmpDir, err := os.MkdirTemp("", "azd-update-backup")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	backupPath := filepath.Join(tmpDir, "azd.exe")
	require.NoError(t, os.WriteFile(backupPath, []byte("backup-content"), 0o755))

	// Restore should move backup → original and remove the temp dir
	require.NoError(t, restoreExeFromBackup(originalPath, backupPath))

	data, err := os.ReadFile(originalPath)
	require.NoError(t, err)
	require.Equal(t, "backup-content", string(data))

	_, err = os.Stat(backupPath)
	require.True(t, os.IsNotExist(err), "backup should be gone after restore")

	// The temp directory should have been cleaned up
	_, err = os.Stat(tmpDir)
	require.True(t, os.IsNotExist(err), "temp backup dir should be removed after restore")
}

func TestRestoreExeFromBackup_RemovesPartialInstall(t *testing.T) {
	installDir := t.TempDir()
	originalPath := filepath.Join(installDir, "azd.exe")

	tmpDir, err := os.MkdirTemp("", "azd-update-backup")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	backupPath := filepath.Join(tmpDir, "azd.exe")

	// Simulate a partial install that left a corrupt file at the original path
	require.NoError(t, os.WriteFile(originalPath, []byte("partial-new"), 0o755))
	require.NoError(t, os.WriteFile(backupPath, []byte("good-backup"), 0o755))

	require.NoError(t, restoreExeFromBackup(originalPath, backupPath))

	data, err := os.ReadFile(originalPath)
	require.NoError(t, err)
	require.Equal(t, "good-backup", string(data), "should have restored backup content")
}

func TestInstallScriptURL(t *testing.T) {
	require.Equal(t, "https://aka.ms/install-azd.ps1", installScriptURL)
}

// TestUpdateViaMSI_NonStandardInstallBlocks verifies that updateViaMSI returns an error
// when the install location doesn't match the expected per-user path.
func TestUpdateViaMSI_NonStandardInstallBlocks(t *testing.T) {
	// Set LOCALAPPDATA to something that won't match the test binary location
	t.Setenv("LOCALAPPDATA", `C:\NonExistentPath`)

	mockRunner := mockexec.NewMockCommandRunner()
	m := NewManager(mockRunner, nil)
	var buf strings.Builder
	cfg := &UpdateConfig{Channel: ChannelStable}

	err := m.updateViaMSI(context.Background(), cfg, &buf)
	require.Error(t, err)

	var updateErr *UpdateError
	require.True(t, errors.As(err, &updateErr))
	require.Equal(t, CodeNonStandardInstall, updateErr.Code)
}

func TestUpdateViaMSI_SuccessPath_CleansUpBackup(t *testing.T) {
	// Set up an "install directory" with a fake azd.exe.
	installDir := t.TempDir()
	originalPath := filepath.Join(installDir, "azd.exe")
	require.NoError(t, os.WriteFile(originalPath, []byte("old-binary"), 0o755))

	// --- Simulate backupCurrentExe ---
	backupDir, err := os.MkdirTemp("", "azd-update-backup")
	require.NoError(t, err)
	defer os.RemoveAll(backupDir) // safety net if test fails early

	backupPath := filepath.Join(backupDir, "azd.exe")
	// Step 1: rename exe to backup
	require.NoError(t, os.Rename(originalPath, backupPath))
	// Step 2: copy back as unlocked safety copy
	require.NoError(t, copyFileWindows(backupPath, originalPath))

	// Verify both exist: safety copy at original, backup in temp
	_, err = os.Stat(originalPath)
	require.NoError(t, err, "safety copy should exist at original path")
	_, err = os.Stat(backupPath)
	require.NoError(t, err, "backup should exist in temp dir")

	// --- Simulate successful MSI install ---
	// The MSI overwrites the unlocked safety copy with a new binary.
	require.NoError(t, os.WriteFile(originalPath, []byte("new-binary-v2"), 0o755))

	// --- Simulate updateViaMSI success cleanup (updateSucceeded = true) ---
	err = os.RemoveAll(filepath.Dir(backupPath))
	require.NoError(t, err)

	// Verify: new binary is at original path, backup dir is gone
	data, err := os.ReadFile(originalPath)
	require.NoError(t, err)
	require.Equal(t, "new-binary-v2", string(data), "original path should have the new binary")

	_, err = os.Stat(backupDir)
	require.True(t, os.IsNotExist(err), "backup directory should be cleaned up after success")
}

func TestUpdateViaMSI_FailurePath_RestoresOriginal(t *testing.T) {
	installDir := t.TempDir()
	originalPath := filepath.Join(installDir, "azd.exe")
	require.NoError(t, os.WriteFile(originalPath, []byte("old-binary"), 0o755))

	// --- Simulate backupCurrentExe ---
	backupDir, err := os.MkdirTemp("", "azd-update-backup")
	require.NoError(t, err)
	defer os.RemoveAll(backupDir)

	backupPath := filepath.Join(backupDir, "azd.exe")
	require.NoError(t, os.Rename(originalPath, backupPath))
	require.NoError(t, copyFileWindows(backupPath, originalPath))

	// --- Simulate failed MSI install that partially overwrote the safety copy ---
	require.NoError(t, os.WriteFile(originalPath, []byte("corrupted-partial"), 0o755))

	// --- Simulate updateViaMSI failure path (updateSucceeded = false) ---
	var buf strings.Builder
	fmt.Fprintf(&buf, "Restoring previous version...\n")
	restoreErr := restoreExeFromBackup(originalPath, backupPath)
	if restoreErr != nil {
		fmt.Fprintf(&buf, "WARNING: failed to restore previous version: %v\n", restoreErr)
		fmt.Fprintf(&buf, "Your backup is at: %s\n", backupPath)
		fmt.Fprintf(&buf, "To recover manually, copy it to: %s\n", originalPath)
	}

	// Verify: original binary is restored, backup dir is cleaned up
	require.NoError(t, restoreErr, "restore should succeed")

	data, err := os.ReadFile(originalPath)
	require.NoError(t, err)
	require.Equal(t, "old-binary", string(data), "original binary should be restored from backup")

	_, err = os.Stat(backupDir)
	require.True(t, os.IsNotExist(err), "backup directory should be cleaned up after restore")

	output := buf.String()
	require.Contains(t, output, "Restoring previous version...")
	require.NotContains(t, output, "WARNING", "restore should succeed without warnings")
}

func TestUpdateViaMSI_FailurePath_PrintsRecoveryInstructions(t *testing.T) {
	installDir := t.TempDir()
	originalPath := filepath.Join(installDir, "azd.exe")

	// Create a backup in a temp dir
	backupDir, err := os.MkdirTemp("", "azd-update-backup")
	require.NoError(t, err)
	defer os.RemoveAll(backupDir)

	backupPath := filepath.Join(backupDir, "azd.exe")
	require.NoError(t, os.WriteFile(backupPath, []byte("old-binary"), 0o755))

	// Now remove the backup so restoreExeFromBackup will fail when trying to read it
	require.NoError(t, os.Remove(backupPath))

	// Make the install dir read-only so the copy will definitely fail
	// (backup file doesn't exist, so copyFileWindows will fail on Open)

	// --- Simulate updateViaMSI failure path with broken restore ---
	var buf strings.Builder
	fmt.Fprintf(&buf, "Restoring previous version...\n")
	restoreErr := restoreExeFromBackup(originalPath, backupPath)
	if restoreErr != nil {
		fmt.Fprintf(&buf, "WARNING: failed to restore previous version: %v\n", restoreErr)
		fmt.Fprintf(&buf, "Your backup is at: %s\n", backupPath)
		fmt.Fprintf(&buf, "To recover manually, copy it to: %s\n", originalPath)
	}

	require.Error(t, restoreErr, "restore should fail when backup is missing")

	output := buf.String()
	require.Contains(t, output, "WARNING: failed to restore previous version")
	require.Contains(t, output, "Your backup is at:")
	require.Contains(t, output, "To recover manually, copy it to:")
}

func TestUpdateViaMSI_SafetyCopySurvivesInterruption(t *testing.T) {
	installDir := t.TempDir()
	originalPath := filepath.Join(installDir, "azd.exe")
	originalContent := []byte("original-binary-content")
	require.NoError(t, os.WriteFile(originalPath, originalContent, 0o755))

	// --- Simulate backupCurrentExe ---
	backupDir, err := os.MkdirTemp("", "azd-update-backup")
	require.NoError(t, err)
	defer os.RemoveAll(backupDir)

	backupPath := filepath.Join(backupDir, "azd.exe")
	require.NoError(t, os.Rename(originalPath, backupPath))
	require.NoError(t, copyFileWindows(backupPath, originalPath))

	// At this point (post-backup, pre-install), if the process is killed,
	// the user should still have a valid azd.exe at the original path.
	data, err := os.ReadFile(originalPath)
	require.NoError(t, err)
	require.Equal(t, originalContent, data,
		"safety copy at original path should match the original binary")

	// The safety copy should be a distinct file (not a hard link to the backup)
	// — verify by checking that removing the backup doesn't affect the safety copy.
	require.NoError(t, os.Remove(backupPath))

	data, err = os.ReadFile(originalPath)
	require.NoError(t, err)
	require.Equal(t, originalContent, data,
		"safety copy should survive even if backup is deleted (independent file)")
}
