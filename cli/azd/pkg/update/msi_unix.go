// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package update

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
)

// checkOtherAzdProcesses is a no-op on non-Windows platforms.
func checkOtherAzdProcesses(_ context.Context, _ exec.CommandRunner) error {
	return nil
}

// isStandardMSIInstall is a no-op on non-Windows platforms.
func isStandardMSIInstall() error {
	return nil
}

// currentExePath is a no-op stub on non-Windows platforms.
func currentExePath() (string, error) {
	return "", nil
}

// backupCurrentExe is a no-op stub on non-Windows platforms.
func backupCurrentExe() (string, string, error) {
	return "", "", nil
}

// restoreExeFromBackup is a no-op stub on non-Windows platforms.
func restoreExeFromBackup(_, _ string) error { return nil }

// cleanupOldBackups is a no-op stub on non-Windows platforms.
func cleanupOldBackups(_ string) {}

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

// buildInstallScriptArgs is a no-op on non-Windows platforms.
func buildInstallScriptArgs(_ Channel) []string {
	return nil
}
