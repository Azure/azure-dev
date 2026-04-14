// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package scripting

import (
	"os/exec"
	"strings"
	"syscall"
)

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
