// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/containerref"
)

type hostedContainerDockerConfig struct {
	imagePassthrough bool
	remoteBuild      bool
}

func validateHostedContainerImage(image string) error {
	image = strings.TrimSpace(image)
	if image == "" || containerref.IsFullyQualified(image) {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		fmt.Sprintf(
			"invalid pre-built image %q: must be in format registry/image[:tag]",
			image,
		),
		"Provide a fully qualified image URL like 'myacr.azurecr.io/agent:v1'",
	)
}

func resolveHostedContainerDockerConfig(
	image string,
	networkInjected bool,
) (hostedContainerDockerConfig, error) {
	image = strings.TrimSpace(image)
	if err := validateHostedContainerImage(image); err != nil {
		return hostedContainerDockerConfig{}, err
	}
	if image != "" {
		return hostedContainerDockerConfig{imagePassthrough: true}, nil
	}

	return hostedContainerDockerConfig{remoteBuild: !networkInjected}, nil
}

func dockerProjectOptionsForHostedContainer(
	image string,
	networkInjected bool,
) (*azdext.DockerProjectOptions, error) {
	config, err := resolveHostedContainerDockerConfig(image, networkInjected)
	if err != nil {
		return nil, err
	}
	options := &azdext.DockerProjectOptions{RemoteBuild: config.remoteBuild}
	if config.imagePassthrough {
		options.ImagePassthrough = true
	}
	return options, nil
}

func dockerProjectMapForHostedContainer(image string, networkInjected bool) (map[string]any, error) {
	config, err := resolveHostedContainerDockerConfig(image, networkInjected)
	if err != nil {
		return nil, err
	}
	if config.imagePassthrough {
		return map[string]any{"imagePassthrough": true}, nil
	}
	return map[string]any{"remoteBuild": config.remoteBuild}, nil
}
