// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/AlecAivazis/survey/v2"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	azdExec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/cli/browser"
)

// CmdAnnotations on a command
type CmdAnnotations map[string]string

type Asker func(p survey.Prompt, response interface{}) error

func azurePortalLink(portalUrlBase, subscriptionId, resourceGroupName string) string {
	if subscriptionId == "" || resourceGroupName == "" {
		return ""
	}
	return output.WithLinkFormat(fmt.Sprintf(
		"%s/#@/resource/subscriptions/%s/resourceGroups/%s/overview",
		portalUrlBase,
		subscriptionId,
		resourceGroupName))
}

func serviceNameWarningCheck(console input.Console, serviceNameFlag string, commandName string) {
	if serviceNameFlag == "" {
		return
	}

	fmt.Fprintln(
		console.Handles().Stderr,
		output.WithWarningFormat("WARNING: The `--service` flag is deprecated and will be removed in a future release."),
	)
	fmt.Fprintf(console.Handles().Stderr, "Next time use `azd %s <service>`.\n\n", commandName)
}

func getTargetServiceName(
	ctx context.Context,
	projectManager project.ProjectManager,
	importManager *project.ImportManager,
	projectConfig *project.ProjectConfig,
	commandName string,
	targetServiceName string,
	allFlagValue bool,
) (string, error) {
	if allFlagValue && targetServiceName != "" {
		return "", fmt.Errorf("cannot specify both --all and <service>")
	}

	if !allFlagValue && targetServiceName == "" {
		targetService, err := projectManager.DefaultServiceFromWd(ctx, projectConfig)
		if errors.Is(err, project.ErrNoDefaultService) {
			return "", fmt.Errorf(
				"current working directory is not a project or service directory. Specify a service name to %s a service, "+
					"or specify --all to %s all services",
				commandName,
				commandName,
			)
		} else if err != nil {
			return "", err
		}

		if targetService != nil {
			targetServiceName = targetService.Name
		}
	}

	if targetServiceName != "" {
		if has, err := importManager.HasService(ctx, projectConfig, targetServiceName); err != nil {
			return "", err
		} else if !has {
			return "", fmt.Errorf("service name '%s' doesn't exist", targetServiceName)
		}
	}

	return targetServiceName, nil
}

// Calculate the total time since t, excluding user interaction time.
func since(t time.Time) time.Duration {
	userInteractTime := tracing.InteractTimeMs.Load()
	return time.Since(t) - time.Duration(userInteractTime)*time.Millisecond
}

// BrowseUrl allow users to override the default browser from the cmd package with some external implementation.
type browseUrl func(ctx context.Context, console input.Console, url string)

// OverrideBrowser allows users to set their own implementation for browsing urls.
var overrideBrowser browseUrl

func openWithDefaultBrowser(ctx context.Context, console input.Console, url string) {
	if overrideBrowser != nil {
		overrideBrowser(ctx, console, url)
		return
	}

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

	log.Printf("warning: failed to use manual launch: %s\n", err.Error())
	console.Message(ctx, fmt.Sprintf("Azd was unable to open the next url. Please try it manually: %s", url))
}

const cReferenceDocumentationUrl = "https://learn.microsoft.com/azure/developer/azure-developer-cli/reference#"
