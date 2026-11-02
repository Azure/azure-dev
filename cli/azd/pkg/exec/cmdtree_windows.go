// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exec

import "syscall"

func (c *CmdTree) setCommandLine() {
	if c.WindowsCommandLine == "" {
		return
	}
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	} else {
		attributes := *c.SysProcAttr
		c.SysProcAttr = &attributes
	}
	c.SysProcAttr.CmdLine = c.WindowsCommandLine
}
