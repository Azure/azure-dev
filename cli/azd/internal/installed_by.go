package internal

import (
	"os"
	"path/filepath"
	"strings"
)

const cInstalledByFileName = ".installed-by.txt"

func GetRawInstalledBy() string {
	exePath, err := os.Executable()

	if err != nil {
		return ""
	}

	resolvedPath, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		return ""
	}

	exeDir := filepath.Dir(resolvedPath)
	installedByFile := filepath.Join(exeDir, cInstalledByFileName)

	bytes, err := os.ReadFile(installedByFile)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(bytes))
}
