// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

func isContainerRuntime(toolName string) bool {
	return toolName == "Docker" || toolName == "Podman"
}

// suggestRemoteBuild limits container-specific advice to services that required a failed container tool.
func suggestRemoteBuild(
	svcTools []svcToolInfo,
	toolErr *tools.MissingToolErrors,
) *internal.ErrorWithSuggestion {
	if !slices.ContainsFunc(toolErr.ToolNames, isContainerRuntime) {
		return nil
	}

	var remoteBuildCapable []string
	for _, info := range svcTools {
		if slices.ContainsFunc(info.tools, func(tool tools.ExternalTool) bool {
			return isContainerRuntime(tool.Name()) && slices.Contains(toolErr.ToolNames, tool.Name())
		}) {
			remoteBuildCapable = append(remoteBuildCapable, info.svc.Name)
		}
	}
	if len(remoteBuildCapable) == 0 {
		return nil
	}

	var unavailable bool
	for i, toolName := range toolErr.ToolNames {
		if !isContainerRuntime(toolName) || i >= len(toolErr.Errs) {
			continue
		}
		err := toolErr.Errs[i]
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		if _, ok := errors.AsType[*docker.ContainerEngineUnavailableError](err); ok {
			unavailable = true
		} else if strings.Contains(err.Error(), "is not running") {
			// External targets may still report an untyped runtime error.
			unavailable = true
		}
	}

	serviceList := strings.Join(remoteBuildCapable, ", ")
	var suggestion string
	if unavailable {
		suggestion = fmt.Sprintf(
			"Services [%s] can build on Azure instead of locally.\n"+
				"Set 'remoteBuild: true' under the 'docker:' section for each service in azure.yaml,\n"+
				"or check that your container runtime (Docker/Podman) is running and accessible.",
			serviceList)
	} else {
		suggestion = fmt.Sprintf(
			"Services [%s] can build on Azure instead of locally.\n"+
				"Set 'remoteBuild: true' under the 'docker:' section for each service in azure.yaml,\n"+
				"or install Docker (https://aka.ms/azure-dev/docker-install)\n"+
				"or Podman (https://aka.ms/azure-dev/podman-install).",
			serviceList)
	}
	return &internal.ErrorWithSuggestion{Err: toolErr, Suggestion: suggestion}
}
