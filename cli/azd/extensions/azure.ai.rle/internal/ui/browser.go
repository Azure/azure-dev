// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ui

import (
	"os/exec"
	"runtime"
)

// OpenBrowser opens a URL in the platform default browser.
var OpenBrowser = openBrowser

func openBrowser(url string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "windows":
		command = "run" + "dll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		command = "open"
		args = []string{url}
	default:
		command = "xdg-open"
		args = []string{url}
	}
	return exec.Command(command, args...).Start() //nolint:gosec // command is selected from fixed platform defaults; url is passed as an argument.
}
