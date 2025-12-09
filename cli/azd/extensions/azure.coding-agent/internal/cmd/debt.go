// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	azdExec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/cli/browser"
)

// this is an internal func from 'azd'. Copied here so we can use it in the extension.

func openWithDefaultBrowser(ctx context.Context, console input.Console, url string) {
	cmdRunner := azdExec.NewCommandRunner(nil)

	// In Codespaces and devcontainers a $BROWSER environment variable is
	// present whose value is an executable that launches the browser when
	// called with the form:
	// $BROWSER <url>
	const BrowserEnvVarName = "BROWSER"

	if envBrowser := os.Getenv(BrowserEnvVarName); len(envBrowser) > 0 {
		_, err := cmdRunner.Run(ctx, azdExec.RunArgs{
			Cmd:  envBrowser,
			Args: []string{url},
		})
		if err == nil {
			return
		}
		log.Printf(
			"warning: failed to open browser configured by $BROWSER: %s\nTrying with default browser.\n",
			err.Error(),
		)
	}

	err := browser.OpenURL(url)
	if err == nil {
		return
	}

	log.Printf(
		"warning: failed to open default browser: %s\nTrying manual launch.", err.Error(),
	)

	// wsl manual launch. Trying cmd first, and pwsh second
	_, err = cmdRunner.Run(ctx, azdExec.RunArgs{
		Cmd: "cmd.exe",
		// cmd notes:
		// /c -> run command
		// /s -> quoted string, use the text within the quotes as it is
		// Replace `&` for `^&` -> cmd require to scape the & to avoid using it as a command concat
		Args: []string{
			"/s", "/c", fmt.Sprintf("start %s", strings.ReplaceAll(url, "&", "^&")),
		},
	})
	if err == nil {
		return
	}
	log.Printf(
		"warning: failed to open browser with cmd: %s\nTrying powershell.", err.Error(),
	)

	_, err = cmdRunner.Run(ctx, azdExec.RunArgs{
		Cmd: "powershell.exe",
		Args: []string{
			"-NoProfile", "-Command", "Start-Process", fmt.Sprintf("\"%s\"", url),
		},
	})
	if err == nil {
		return
	}

	log.Printf("warning: failed to use manual launch: %v\n", err)
	console.Message(ctx, fmt.Sprintf("Azd was unable to open the next url. Please try it manually: %s", url))
}
