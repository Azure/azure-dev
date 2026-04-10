// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package scripting

import "os/exec"

func setCmdLineOverride(_ *exec.Cmd, _ []string, _ bool) {}