// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type hostedContainerDockerConfig struct {
	imagePassthrough bool
	remoteBuild      bool
}

func resolveHostedContainerDockerConfig(image string, networkInjected bool) hostedContainerDockerConfig {
	if strings.TrimSpace(image) != "" {
		return hostedContainerDockerConfig{imagePassthrough: true}
	}

	return hostedContainerDockerConfig{remoteBuild: !networkInjected}
}

func dockerProjectOptionsForHostedContainer(image string, networkInjected bool) *azdext.DockerProjectOptions {
	config := resolveHostedContainerDockerConfig(image, networkInjected)
	options := &azdext.DockerProjectOptions{RemoteBuild: config.remoteBuild}
	if config.imagePassthrough {
		options.ImagePassthrough = true
	}
	return options
}

func dockerProjectMapForHostedContainer(image string, networkInjected bool) map[string]any {
	config := resolveHostedContainerDockerConfig(image, networkInjected)
	if config.imagePassthrough {
		return map[string]any{"imagePassthrough": true}
	}
	return map[string]any{"remoteBuild": config.remoteBuild}
}
