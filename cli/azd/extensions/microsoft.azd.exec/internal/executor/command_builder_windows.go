// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package executor

import (
	"os/exec"
	"strings"
	"syscall"
)

// setCmdLineOverride sets the raw command line for cmd.exe on Windows to bypass
// Go's CommandLineToArgvW argument escaping which is incompatible with cmd.exe
// command-line parsing. When wrapOuter is true, the /c payload is wrapped in
// outer quotes to protect file paths with metacharacters. When false (inline
// mode), args are joined without outer wrapping so the script content is
// interpreted by cmd.exe as-is.
func setCmdLineOverride(cmd *exec.Cmd, args []string, wrapOuter bool) {
	payload := strings.Join(args[2:], " ")
	var cmdLine string
	if wrapOuter {
		cmdLine = args[0] + " " + args[1] + ` "` + payload + `"`
	} else {
		cmdLine = args[0] + " " + args[1] + " " + payload
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: cmdLine,
	}
}
