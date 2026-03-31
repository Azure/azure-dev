// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package executor

import "os/exec"

// setCmdLineOverride is a no-op on non-Windows platforms.
func setCmdLineOverride(_ *exec.Cmd, _ []string, _ bool) {}
