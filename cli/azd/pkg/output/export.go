// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

type ExportFormatter struct{}

type ShellType string

const (
	BashShell    ShellType = "bash"
	ZshShell     ShellType = "zsh"
	FishShell    ShellType = "fish"
	PwshShell    ShellType = "pwsh"
	CmdShell     ShellType = "cmd"
	DefaultShell ShellType = "default"
)

func (f *ExportFormatter) Kind() Format {
	return ExportFormat
}

func detectShell() (ShellType, error) {
	// Check environment variable SHELL for Unix-like systems
	shell := os.Getenv("SHELL")
	if shell == "" {
		// Check environment variable ComSpec for Windows systems
		shell = os.Getenv("ComSpec")
		if shell != "" {
			if strings.Contains(strings.ToLower(shell), "cmd") {
				return CmdShell, nil
			}
		}
		// Check environment variable PSModulePath for PowerShell
		if os.Getenv("PSModulePath") != "" {
			return PwshShell, nil
		}
		if runtime.GOOS == "windows" {
			return CmdShell, nil
		} else {
			return DefaultShell, fmt.Errorf("could not detect shell")
		}
	}

	if strings.Contains(shell, "bash") {
		return BashShell, nil
	} else if strings.Contains(shell, "zsh") {
		return ZshShell, nil
	} else if strings.Contains(shell, "fish") {
		return FishShell, nil
	} else {
		return DefaultShell, fmt.Errorf("unsupported shell: %s", shell)
	}
}

func (f *ExportFormatter) Format(obj interface{}, writer io.Writer, _ interface{}) error {
	values, ok := obj.(map[string]string)
	if !ok {
		return fmt.Errorf("ExportFormatter can only format objects of type map[string]string")
	}

	shell, err := detectShell()
	if err != nil && shell == DefaultShell {
		// Fallback to default shell based on OS
		if runtime.GOOS == "windows" {
			shell = CmdShell
		} else {
			shell = BashShell
		}
	}

	var content string
	for key, value := range values {
		switch shell {
		case BashShell, ZshShell:
			content += fmt.Sprintf("export %s=\"%s\"\n", key, value)
		case FishShell:
			content += fmt.Sprintf("set -x %s \"%s\"\n", key, value)
		case PwshShell:
			content += fmt.Sprintf("$Env:%s = \"%s\"\n", key, value)
		case CmdShell:
			content += fmt.Sprintf("set %s=%s\n", key, value)
		default:
			return fmt.Errorf("unsupported shell type: %s", shell)
		}
	}

	_, err = writer.Write([]byte(content))
	if err != nil {
		return fmt.Errorf("could not write content: %w", err)
	}

	return nil
}

var _ Formatter = (*ExportFormatter)(nil)
