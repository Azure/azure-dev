// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package update

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestMSICommandLine(t *testing.T) {
	tests := []struct {
		name string
		dir  string
	}{
		{"spaces", `C:\Program Files\Azure Dev CLI`},
		{"special characters", `D:\O'Connor [1] & Tools\azd`},
		{"equals in path", `D:\Tools=Shared\azd`},
		{"volume root", `D:\`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := `C:\Windows\System32\msiexec.exe`
			args := []string{"/i", `C:\MSI Packages\azd.msi`, "/qn",
				"/L*v", `C:\MSI Logs\install.log`, "ALLUSERS=1", "INSTALLDIR=" + tt.dir}
			require.Equal(t,
				`C:\Windows\System32\msiexec.exe /i "C:\MSI Packages\azd.msi" /qn `+
					`/L*v "C:\MSI Logs\install.log" ALLUSERS=1 INSTALLDIR="`+tt.dir+`"`,
				msiCommandLine(command, args))
		})
	}
}

func TestMSICommandLineNativeParser(t *testing.T) {
	systemDir, err := windows.GetSystemDirectory()
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "Program Files & Tools")
	// This package deliberately does not exist. MSI can parse the command line,
	// but cannot install anything, so this test never changes installed products.
	packagePath := filepath.Join(dir, "missing.msi")
	logPath := filepath.Join(t.TempDir(), "install.log")
	args := exec.NewRunArgs(filepath.Join(systemDir, "msiexec.exe"),
		"/i", packagePath, "/qn", "/norestart", "/L*v", logPath,
		"ALLUSERS=1", "INSTALLDIR="+dir, "MSIRESTARTMANAGERCONTROL=DisableShutdown").
		WithShell(false)
	args.WindowsCommandLine = msiCommandLine(args.Cmd, args.Args)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	result, err := exec.NewCommandRunner(nil).Run(ctx, args)
	require.Error(t, err)
	require.Equal(t, int(windows.ERROR_INSTALL_PACKAGE_OPEN_FAILED), result.ExitCode,
		"MSI must accept the command line and reject only the nonexistent package, not show usage with error 1639")
}
