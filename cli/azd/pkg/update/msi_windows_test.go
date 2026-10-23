// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
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

func TestBuildInstallScriptArgs(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\testuser\AppData\Local`)
	expectedDir := expectedPerUserInstallDir()

	tests := []struct {
		name    string
		channel Channel
		version string
		wantErr bool
		// We check that certain substrings appear in the constructed args
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:    "stable",
			channel: ChannelStable,
			version: "1.28.1",
			wantContains: []string{
				"-NoProfile",
				"-ExecutionPolicy", "Bypass",
				"-Command",
				installScriptURL,
				"-Version '1.28.1'",
				"Remove-Item",
				"$env:PSModulePath",
			},
			wantNotContains: []string{
				"-InstallFolder",
				"-SkipVerify",
			},
		},
		{
			// Rejected rather than defaulted: falling back to the rolling stable folder
			// would be the unpinned install pinning exists to prevent. Unreachable in
			// practice, since VersionInfo.validate rejects the channel first.
			name:    "unknown channel is an error",
			channel: Channel("nightly"),
			wantErr: true,
		},
		{
			name:    "daily",
			channel: ChannelDaily,
			version: "1.29.0-beta.1-daily.6614998",
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
			args, err := buildInstallScriptArgs(&VersionInfo{Channel: tt.channel, Version: tt.version})
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, args)
				return
			}
			require.NoError(t, err)
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

	args, err := buildInstallScriptArgs(&VersionInfo{Channel: ChannelDaily, Version: "1.29.0-beta.1-daily.6614998"})
	require.NoError(t, err)
	script := args[4]

	// The apostrophe must be doubled for a valid PowerShell single-quoted string.
	require.Contains(t, script, `O''Connor`)
	// Must NOT contain unescaped apostrophe inside the -InstallFolder value.
	require.NotContains(t, script, `-InstallFolder 'C:\Users\O'Connor`)
}

func TestBuildInstallScriptArgs_Structure(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\testuser\AppData\Local`)
	expectedDir := expectedPerUserInstallDir()

	args, err := buildInstallScriptArgs(&VersionInfo{Channel: ChannelStable, Version: "1.28.1"})
	require.NoError(t, err)

	require.Equal(t, 5, len(args), "expected exactly 5 args")
	require.Equal(t, "-NoProfile", args[0])
	require.Equal(t, "-ExecutionPolicy", args[1])
	require.Equal(t, "Bypass", args[2])
	require.Equal(t, "-Command", args[3])

	// Stable downloads to temp file — pinned to the resolved version
	script := args[4]
	require.True(t, strings.HasPrefix(script, "$env:PSModulePath"), "script should start with PSModulePath reset")
	require.Contains(t, script, "Invoke-RestMethod")
	require.Contains(t, script, installScriptURL)
	require.Contains(t, script, "Remove-Item")
	require.Contains(t, script, "-Version '1.28.1'")
	require.NotContains(t, script, "-InstallFolder")

	// Daily downloads to temp file with -Version 'daily'
	argsDaily, err := buildInstallScriptArgs(&VersionInfo{Channel: ChannelDaily, Version: "1.29.0-beta.1-daily.6614998"})
	require.NoError(t, err)
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

func TestUpdateViaMSI_NonStandardInstallBlocks(t *testing.T) {
	// Set LOCALAPPDATA to something that won't match the test binary location
	t.Setenv("LOCALAPPDATA", `C:\NonExistentPath`)

	mockRunner := mockexec.NewMockCommandRunner()
	m := NewManager(mockRunner, nil)
	var buf strings.Builder
	target := &VersionInfo{Channel: ChannelStable, Version: "1.28.1"}

	err := m.updateViaMSI(t.Context(), target, &buf)
	require.Error(t, err)

	var updateErr *UpdateError
	require.True(t, errors.As(err, &updateErr))
	require.Equal(t, CodeNonStandardInstall, updateErr.Code)
}

func TestResolveMSIInstallation(t *testing.T) {
	userDir := `C:\Users\test\AppData\Local\Programs\Azure Dev CLI`
	tests := []struct {
		name       string
		exePath    string
		machineDir string
		userDir    string
		perMachine bool
		wantErr    bool
	}{
		{"default user", userDir + `\azd.exe`, "", userDir, false, false},
		{"user with machine install", userDir + `\azd.exe`, `C:\Program Files\Azure Dev CLI`, userDir, false, false},
		{"machine", `C:\Program Files\Azure Dev CLI\azd.exe`, `C:\Program Files\Azure Dev CLI`, userDir, true, false},
		{"custom machine", `D:\Tools\azd\azd.exe`, `D:\Tools\azd`, userDir, true, false},
		{"case insensitive", `D:\TOOLS\azd\azd.exe`, `d:\tools\azd\`, userDir, true, false},
		{"machine without LOCALAPPDATA", `D:\Tools\azd\azd.exe`, `D:\Tools\azd`, "", true, false},
		{"machine takes precedence", userDir + `\azd.exe`, userDir, userDir, true, false},
		{"unregistered Program Files", `C:\Program Files\Azure Dev CLI\azd.exe`, "", userDir, false, true},
		{"different executable", `D:\Other\azd.exe`, `D:\Tools\azd`, userDir, false, true},
		{"unregistered custom user", `C:\Tools\azd.exe`, "", userDir, false, true},
		{"unknown without LOCALAPPDATA", `D:\Other\azd.exe`, "", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			install, err := resolveMSIInstallation(tt.exePath, tt.machineDir, tt.userDir)
			if tt.wantErr {
				require.Error(t, err)
				var updateErr *UpdateError
				require.ErrorAs(t, err, &updateErr)
				require.Equal(t, CodeNonStandardInstall, updateErr.Code)
				require.Nil(t, install)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.exePath, install.exePath)
			require.Equal(t, tt.perMachine, install.perMachine)
		})
	}
}

func TestMSIInstallationValidate(t *testing.T) {
	tests := []struct {
		name           string
		perMachine     bool
		currentChannel Channel
		targetChannel  Channel
		elevated       bool
		code           string
	}{
		{"elevated stable machine", true, ChannelStable, ChannelStable, true, ""},
		{"unelevated machine", true, ChannelStable, ChannelStable, false, CodeElevationRequired},
		{"machine to daily", true, ChannelStable, ChannelDaily, true, CodeUnsupportedInstallMethod},
		{"daily machine to stable", true, ChannelDaily, ChannelStable, true, CodeUnsupportedInstallMethod},
		{"daily machine", true, ChannelDaily, ChannelDaily, true, CodeUnsupportedInstallMethod},
		{"user stable", false, ChannelStable, ChannelStable, false, ""},
		{"user to daily", false, ChannelStable, ChannelDaily, false, ""},
		{"user to stable", false, ChannelDaily, ChannelStable, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			install := &msiInstallation{exePath: `D:\Shared Tools\azd.exe`, perMachine: tt.perMachine}
			err := install.validate(&VersionInfo{Channel: tt.targetChannel}, tt.currentChannel, tt.elevated)
			wantCode := tt.code
			if tt.perMachine && runtime.GOARCH != "amd64" {
				wantCode = CodeUnsupportedInstallMethod
			}
			if wantCode == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				var updateErr *UpdateError
				require.ErrorAs(t, err, &updateErr)
				require.Equal(t, wantCode, updateErr.Code)
				if wantCode == CodeElevationRequired {
					suggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
					require.True(t, ok, "elevation errors must carry a suggestion")
					require.Contains(t, suggestion.Suggestion, "Run as administrator")
					require.Contains(t, suggestion.Suggestion, "azd update --channel stable")
					require.NotContains(t, suggestion.Suggestion, install.exePath)
					require.Contains(t, err.Error(), filepath.Dir(install.exePath))
				}
			}
		})
	}
}

func TestVerifyAuthenticodeLiteralPath(t *testing.T) {
	powershell, err := systemPowerShell()
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "O'Connor [1] & Tools")
	require.NoError(t, os.Mkdir(dir, 0755))
	signedFile := filepath.Join(dir, "signed.exe")
	require.NoError(t, copyFileWindows(powershell, signedFile))

	var output bytes.Buffer
	manager := NewManager(exec.NewCommandRunner(nil), nil)
	require.NoError(t, manager.verifyAuthenticode(t.Context(), signedFile, &output))
	require.Contains(t, output.String(), "Code signature verified.")
}

func TestLockMSIInstallation(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockMSIInstallation(dir)
	require.NoError(t, err)
	defer unlock()
	_, err = lockMSIInstallation(strings.ToUpper(dir))
	require.ErrorContains(t, err, "another azd update")

	otherUnlock, err := lockMSIInstallation(t.TempDir())
	require.NoError(t, err)
	otherUnlock()
}

func TestBackupExeRunningProcess(t *testing.T) {
	if mode := os.Getenv("AZD_TEST_MSI_BACKUP"); mode != "" {
		exePath, err := currentExePath()
		require.NoError(t, err)
		before, err := hashFile(exePath)
		require.NoError(t, err)
		backup, err := backupExe(exePath, filepath.Dir(exePath))
		require.NoError(t, err)
		require.Equal(t, filepath.Dir(exePath), filepath.Dir(filepath.Dir(backup)))
		after, err := hashFile(exePath)
		require.NoError(t, err)
		require.Equal(t, before, after)
		require.NoError(t, restoreExeFromBackup(exePath, backup))
		var output bytes.Buffer
		cleanMSIBackup(backup, &output)
		if mode == "restricted" {
			require.Contains(t, output.String(), "WARNING: could not start executable backup cleanup")
			require.Contains(t, output.String(), "remove the backup at "+backup+" manually")
		} else {
			require.Empty(t, output.String(), "routine backup cleanup must not print to the console")
		}
		return
	}

	source, err := os.Executable()
	require.NoError(t, err)
	for _, restricted := range []bool{false, true} {
		name := "normal terminal"
		if restricted {
			name = "job prohibits breakaway"
		}
		t.Run(name, func(t *testing.T) {
			installDir := filepath.Join(t.TempDir(), "O'Connor & Tools")
			require.NoError(t, os.Mkdir(installDir, 0755))
			testExe := filepath.Join(installDir, "azd.exe")
			require.NoError(t, copyFileWindows(source, testExe))
			if restricted {
				args := exec.NewRunArgs(testExe, "-test.run=^TestBackupExeRunningProcess$").
					WithShell(false).
					WithEnv([]string{"AZD_TEST_MSI_BACKUP=restricted"})
				result, err := exec.NewCommandRunner(nil).Run(t.Context(), args)
				require.NoError(t, err, "%s%s", result.Stdout, result.Stderr)
				backups, err := filepath.Glob(filepath.Join(installDir, "azd-update-backup*"))
				require.NoError(t, err)
				require.Len(t, backups, 1, "failed cleanup must retain the backup for manual removal")
				return
			}
			//nolint:gosec // Executes a copy of this test binary without a job that prohibits child breakaway.
			cmd := osexec.CommandContext(t.Context(), testExe, "-test.run=^TestBackupExeRunningProcess$")
			cmd.Env = append(os.Environ(), "AZD_TEST_MSI_BACKUP=1")
			result, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", result)
			require.Eventually(t, func() bool {
				backups, err := filepath.Glob(filepath.Join(installDir, "azd-update-backup*"))
				return err == nil && len(backups) == 0
			}, 15*time.Second, 50*time.Millisecond, "cleanup helper must survive the updating process")
		})
	}
}

func TestUpdateMSIMachine(t *testing.T) {
	tests := []struct {
		name            string
		downloadStatus  int
		signatureErr    bool
		cancelBeforeMSI bool
		exitCode        int
		runErr          bool
		newContent      string
		removeInstalled bool
		wantContent     string
		wantCode        string
		restart         bool
		preserveBackup  bool
	}{
		{name: "success", newContent: "new executable", wantContent: "new executable"},
		{name: "download failure", downloadStatus: 404, wantContent: "old executable", wantCode: CodeDownloadFailed},
		{name: "invalid signature", signatureErr: true, wantContent: "old executable", wantCode: CodeSignatureInvalid},
		{name: "canceled before MSI", cancelBeforeMSI: true, wantContent: "old executable", wantCode: CodeReplaceFailed},
		{name: "installer error", exitCode: 1603, runErr: true, newContent: "partial", wantContent: "old executable",
			wantCode: CodeReplaceFailed},
		{name: "nonzero result without error", exitCode: 1618, wantContent: "old executable", wantCode: CodeReplaceFailed},
		{name: "unchanged executable", wantContent: "old executable", wantCode: CodeReplaceFailed, preserveBackup: true},
		{name: "missing executable after MSI success", removeInstalled: true,
			wantCode: CodeReplaceFailed, preserveBackup: true},
		{name: "restart required", exitCode: int(windows.ERROR_SUCCESS_REBOOT_REQUIRED),
			runErr: true, newContent: "new executable",
			wantContent: "new executable", restart: true},
		{name: "deferred replacement", exitCode: int(windows.ERROR_SUCCESS_REBOOT_REQUIRED),
			runErr: true, wantContent: "old executable", restart: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installDir := filepath.Join(t.TempDir(), "O'Connor & Tools")
			require.NoError(t, os.Mkdir(installDir, 0755))
			exePath := filepath.Join(installDir, "azd.exe")
			require.NoError(t, os.WriteFile(exePath, []byte("old executable"), 0600))
			target := &VersionInfo{Channel: ChannelStable, Version: "1.28.1"}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expected := fmt.Sprintf("/azd/standalone/release/1.28.1/azd-windows-%s.msi", runtime.GOARCH)
				if r.URL.Path != expected {
					t.Errorf("unexpected MSI URL path %q, want %q", r.URL.Path, expected)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				if tt.downloadStatus != 0 {
					w.WriteHeader(tt.downloadStatus)
					return
				}
				_, err := w.Write([]byte("signed MSI"))
				require.NoError(t, err)
			}))
			defer server.Close()
			runner := mockexec.NewMockCommandRunner()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var writer bytes.Buffer
			signatureChecked := false
			installerRun := false
			runner.When(func(args exec.RunArgs, command string) bool {
				return filepath.Base(args.Cmd) == "powershell.exe"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				signatureChecked = true
				require.Contains(t, args.Args[2], "O''Connor & Tools")
				require.Contains(t, args.Args[2], "$env:PSModulePath")
				require.Contains(t, args.Args[2], "Get-AuthenticodeSignature -LiteralPath")
				entries, err := filepath.Glob(filepath.Join(installDir, "azd-update-backup*"))
				require.NoError(t, err)
				require.Empty(t, entries, "signature verification must precede executable mutation")
				if tt.signatureErr {
					return exec.RunResult{ExitCode: 1}, errors.New("invalid signature")
				}
				if tt.cancelBeforeMSI {
					cancel()
				}
				return exec.RunResult{}, nil
			})
			runner.When(func(args exec.RunArgs, command string) bool {
				return filepath.Base(args.Cmd) == "msiexec.exe"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				installerRun = true
				require.True(t, signatureChecked)
				require.Equal(t, []string{
					"/i", args.Args[1], "/qn", "/norestart", "/L*v", args.Args[5],
					"ALLUSERS=1", "INSTALLDIR=" + installDir, "MSIRESTARTMANAGERCONTROL=DisableShutdown",
				}, args.Args)
				require.Equal(t, installDir, filepath.Dir(filepath.Dir(args.Args[1])))
				require.Same(t, &writer, args.StdOut)
				require.Same(t, &writer, args.Stderr)
				require.NoError(t, os.WriteFile(args.Args[5], []byte("MSI diagnostics"), 0600))
				if tt.newContent != "" {
					require.NoError(t, os.WriteFile(exePath, []byte(tt.newContent), 0600))
				}
				if tt.removeInstalled {
					require.NoError(t, os.Remove(exePath))
				}
				var err error
				if tt.runErr {
					err = errors.New("installer exit")
				}
				return exec.RunResult{ExitCode: tt.exitCode}, err
			})
			manager := NewManager(runner, testClientWithRewrite(server.URL))
			installErr := manager.updateMSI(ctx, target, &msiInstallation{
				exePath: exePath, perMachine: true,
			}, &writer)
			switch {
			case tt.restart:
				require.ErrorIs(t, installErr, ErrRestartRequired)
				require.NotContains(t, writer.String(), "Restoring")
			case tt.wantCode != "":
				require.Error(t, installErr)
				var updateErr *UpdateError
				require.ErrorAs(t, installErr, &updateErr)
				require.Equal(t, tt.wantCode, updateErr.Code)
			default:
				require.NoError(t, installErr)
			}
			content, err := os.ReadFile(exePath)
			if tt.removeInstalled {
				require.ErrorIs(t, err, os.ErrNotExist, "verification failure must not restore the old executable")
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.wantContent, string(content))
			}
			if tt.preserveBackup {
				require.NotContains(t, writer.String(), "Restoring")
				require.Contains(t, writer.String(), "Original executable retained at")
			}
			require.Equal(t, tt.downloadStatus == 0, signatureChecked)
			require.Equal(t, tt.downloadStatus == 0 && !tt.signatureErr && !tt.cancelBeforeMSI, installerRun)
			entries, err := os.ReadDir(installDir)
			require.NoError(t, err)
			backupFound := false
			for _, entry := range entries {
				if entry.IsDir() {
					require.True(t, installerRun && (tt.wantCode != "" || tt.restart))
					files, err := os.ReadDir(filepath.Join(installDir, entry.Name()))
					require.NoError(t, err)
					require.Len(t, files, 1)
					if strings.HasPrefix(entry.Name(), "azd-update-backup") {
						backupFound = true
						require.True(t, tt.preserveBackup)
						backup, err := os.ReadFile(filepath.Join(installDir, entry.Name(), files[0].Name()))
						require.NoError(t, err)
						require.Equal(t, "old executable", string(backup))
					} else {
						require.Equal(t, "install.log", files[0].Name())
						require.Contains(t, writer.String(), "MSI log:")
					}
				}
			}
			require.Equal(t, tt.preserveBackup, backupFound)
			if installerRun && tt.exitCode != 0 && !tt.restart {
				suggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](installErr)
				require.True(t, ok, "machine installer failures must explain how to reinstall")
				require.Contains(t, suggestion.Suggestion, "does not restore MSI registration or the system PATH")
				require.Contains(t, suggestion.Suggestion, "ALLUSERS=1")
				require.Contains(t, suggestion.Suggestion, "INSTALLDIR=\""+installDir+"\"")
				require.NotEmpty(t, suggestion.Links)
			}
		})
	}
}

func TestUpdateMSIPerUser(t *testing.T) {
	for _, channel := range []Channel{ChannelStable, ChannelDaily} {
		t.Run(string(channel), func(t *testing.T) {
			t.Setenv("LOCALAPPDATA", t.TempDir())
			dir := expectedPerUserInstallDir()
			require.NoError(t, os.MkdirAll(dir, 0755))
			exePath := filepath.Join(dir, "azd.exe")
			require.NoError(t, os.WriteFile(exePath, []byte("old"), 0600))
			runner := mockexec.NewMockCommandRunner()
			runner.When(func(args exec.RunArgs, command string) bool {
				return args.Cmd == "powershell"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				script := args.Args[4]
				if channel == ChannelStable {
					require.Contains(t, script, "-Version '1.28.1'")
					require.NotContains(t, script, "-InstallFolder")
				} else {
					require.Contains(t, script, "-Version 'daily'")
					require.Contains(t, script, "-InstallFolder '"+escapeForPSSingleQuote(dir)+"'")
				}
				require.NotContains(t, script, "ALLUSERS=1")
				require.NoError(t, os.WriteFile(exePath, []byte("updated"), 0600))
				return exec.RunResult{}, nil
			})
			target := &VersionInfo{Channel: channel, Version: "1.28.1"}
			require.NoError(t, NewManager(runner, nil).updateMSI(
				t.Context(), target, &msiInstallation{exePath: exePath}, &bytes.Buffer{}))
		})
	}
}
