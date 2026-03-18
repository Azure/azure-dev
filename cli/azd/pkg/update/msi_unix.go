// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package update

// isStandardMSIInstall is a no-op on non-Windows platforms.
func isStandardMSIInstall() error {
	return nil
}

// backupCurrentExe is a no-op stub on non-Windows platforms.
func backupCurrentExe() (string, string, error) {
	return "", "", nil
}

// restoreExeFromBackup is a no-op stub on non-Windows platforms.
func restoreExeFromBackup(_, _ string) error { return nil }

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
