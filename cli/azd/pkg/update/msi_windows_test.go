// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

// NOTE: Automated testing of updateViaMSI invoking PowerShell with the correct
// arguments and isStandardMSIInstall succeeding on a standard path are limited
// because go test compiles to temp directories that never match the expected MSI
// install path (Programs\Azure Dev CLI), and backupCurrentExe cannot rename the
// running test binary. We rely on manual testing on actual per-user MSI installs
// to validate these code paths.

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

func TestBuildInstallScriptArgs(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\testuser\AppData\Local`)
	expectedDir := expectedPerUserInstallDir()

	tests := []struct {
		name    string
		channel Channel
		// We check that certain substrings appear in the constructed args
		wantContains    []string
		wantNotContains []string
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
				"Remove-Item",
				"$env:PSModulePath",
			},
			wantNotContains: []string{
				"-InstallFolder",
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
				"-InstallFolder",
				expectedDir,
				"Remove-Item",
				"$env:PSModulePath",
			},
			wantNotContains: []string{
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
			for _, s := range tt.wantNotContains {
				require.NotContains(t, joined, s, "expected args NOT to contain %q", s)
			}
		})
	}
}

func TestEscapeForPSSingleQuote(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no quotes", `C:\Users\testuser`, `C:\Users\testuser`},
		{"single apostrophe", `C:\Users\O'Connor`, `C:\Users\O''Connor`},
		{"multiple apostrophes", `C:\it's\a'path`, `C:\it''s\a''path`},
		{"empty string", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, escapeForPSSingleQuote(tt.input))
		})
	}
}

func TestBuildInstallScriptArgs_ApostropheInPath(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\O'Connor\AppData\Local`)

	args := buildInstallScriptArgs(ChannelDaily)
	script := args[4]

	// The apostrophe must be doubled for a valid PowerShell single-quoted string.
	require.Contains(t, script, `O''Connor`)
	// Must NOT contain unescaped apostrophe inside the -InstallFolder value.
	require.NotContains(t, script, `-InstallFolder 'C:\Users\O'Connor`)
}

func TestBuildInstallScriptArgs_Structure(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\testuser\AppData\Local`)
	expectedDir := expectedPerUserInstallDir()

	args := buildInstallScriptArgs(ChannelStable)

	require.Equal(t, 5, len(args), "expected exactly 5 args")
	require.Equal(t, "-NoProfile", args[0])
	require.Equal(t, "-ExecutionPolicy", args[1])
	require.Equal(t, "Bypass", args[2])
	require.Equal(t, "-Command", args[3])

	// Stable downloads to temp file — passes -Version 'stable' explicitly
	script := args[4]
	require.True(t, strings.HasPrefix(script, "$env:PSModulePath"), "script should start with PSModulePath reset")
	require.Contains(t, script, "Invoke-RestMethod")
	require.Contains(t, script, installScriptURL)
	require.Contains(t, script, "Remove-Item")
	require.Contains(t, script, "-Version 'stable'")
	require.NotContains(t, script, "-InstallFolder")

	// Daily downloads to temp file with -Version 'daily'
	argsDaily := buildInstallScriptArgs(ChannelDaily)
	require.Equal(t, 5, len(argsDaily))
	require.Equal(t, "Bypass", argsDaily[2])
	scriptDaily := argsDaily[4]
	require.True(t, strings.HasPrefix(scriptDaily, "$env:PSModulePath"), "daily script should start with PSModulePath reset")
	require.Contains(t, scriptDaily, "Invoke-RestMethod")
	require.Contains(t, scriptDaily, installScriptURL)
	require.Contains(t, scriptDaily, "-Version 'daily'")
	require.Contains(t, scriptDaily, "-InstallFolder")
	require.Contains(t, scriptDaily, expectedDir)
	require.Contains(t, scriptDaily, "Remove-Item")
}

func TestIsStandardMSIInstall_NonStandardPath(t *testing.T) {
	// Set LOCALAPPDATA to a directory that definitely doesn't match the test binary location
	t.Setenv("LOCALAPPDATA", `C:\SomeOtherLocation`)

	err := isStandardMSIInstall()
	require.Error(t, err)

	var updateErr *UpdateError
	require.ErrorAs(t, err, &updateErr)
	require.Equal(t, CodeNonStandardInstall, updateErr.Code)
	require.Contains(t, err.Error(), "managed by an administrator")
	require.Contains(t, err.Error(), "AZD_SKIP_UPDATE_CHECK=1")
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
func TestRestoreExeFromBackup(t *testing.T) {
	installDir := t.TempDir()
	originalPath := filepath.Join(installDir, "azd.exe")

	// Create a temp backup dir with the backed-up exe
	tmpDir, err := os.MkdirTemp("", "azd-update-backup")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	backupPath := filepath.Join(tmpDir, "azd.exe")
	require.NoError(t, os.WriteFile(backupPath, []byte("backup-content"), 0600))

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
	require.NoError(t, os.WriteFile(originalPath, []byte("partial-new"), 0600))
	require.NoError(t, os.WriteFile(backupPath, []byte("good-backup"), 0600))

	require.NoError(t, restoreExeFromBackup(originalPath, backupPath))

	data, err := os.ReadFile(originalPath)
	require.NoError(t, err)
	require.Equal(t, "good-backup", string(data), "should have restored backup content")
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
