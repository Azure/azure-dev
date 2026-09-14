// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/containerref"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
)

// DeployPreviewImage describes image work a deployment would perform.
// Known is false when a timestamp tag, registry, or other input is not yet known.
type DeployPreviewImage struct {
	Mode             string `json:"mode"`
	Image            string `json:"image,omitempty"`
	Repository       string `json:"repository,omitempty"`
	Tag              string `json:"tag,omitempty"`
	Build            bool   `json:"build"`
	Push             bool   `json:"push"`
	Known            bool   `json:"known"`
	AlternativeImage string `json:"alternativeImage,omitempty"`
}

// HasImageChanges reports whether an image change, build, push, or unresolved
// image reference needs to be included in human-readable deployment output.
func (r *DirectDeployPreviewResult) HasImageChanges() bool {
	if r.Image != nil && (r.Image.Build || r.Image.Push || !r.Image.Known) {
		return true
	}
	for _, group := range r.Changes {
		if group.Group == "containerImage" && len(group.Changes) > 0 {
			return true
		}
	}
	return false
}

const pendingPreviewImage = "(known after build)"

func planPreviewImage(
	definition agent_yaml.ContainerAgent,
	service *azdext.ServiceConfig,
	projectName string,
	environment map[string]string,
) (*DeployPreviewImage, error) {
	if definition.CodeConfiguration != nil || (service == nil && definition.Image == "") {
		return &DeployPreviewImage{Mode: "code", Known: true}, nil
	}
	prebuilt := service == nil || service.GetDocker().GetImagePassthrough() || definition.RegistryConnectionID != ""
	if marker, err := strconv.ParseBool(environment["AZD_AGENT_SKIP_ACR"]); err == nil && marker {
		prebuilt = definition.Image != ""
	}
	if prebuilt {
		if service.GetDocker().GetRemoteBuild() {
			return nil, exterrors.Validation(exterrors.CodeConflictingArguments,
				"image passthrough cannot be combined with docker.remoteBuild",
				"remove docker.remoteBuild when using a prebuilt image")
		}
		image := definition.Image
		if image == "" {
			image = service.GetDocker().GetImage()
		}
		if previewContainsUnknown(image) {
			return &DeployPreviewImage{Mode: "prebuilt"}, nil
		}
		if !containerref.IsValid(image) {
			return nil, exterrors.Validation(exterrors.CodeInvalidServiceConfig,
				"the prebuilt container image is missing or invalid", "set a valid image reference")
		}
		if definition.RegistryConnectionID != "" && !containerref.IsFullyQualified(image) {
			return nil, exterrors.Validation(exterrors.CodeInvalidServiceConfig,
				"registryConnectionId requires a fully qualified container image", "include the registry host in image")
		}
		return &DeployPreviewImage{Mode: "prebuilt", Image: image, Known: true}, nil
	}
	plan := &DeployPreviewImage{Mode: "build", Build: true, Push: true, AlternativeImage: definition.Image}
	expand := func(value string) (string, error) {
		properties, err := resolvePreviewProperties(map[string]any{"value": value}, environment, nil)
		if err != nil {
			return "", err
		}
		expanded, ok := properties["value"].(string)
		if !ok {
			return "", fmt.Errorf("image expression must resolve to a string")
		}
		return expanded, nil
	}
	image, err := expand(service.GetDocker().GetImage())
	if err != nil {
		return nil, fmt.Errorf("resolve docker.image: %w", err)
	}
	if previewContainsUnknown(image) {
		return plan, nil
	}
	if image == "" {
		envName := environment["AZURE_ENV_NAME"]
		if projectName == "" || service.Name == "" || envName == "" {
			return plan, nil
		}
		image = strings.ToLower(fmt.Sprintf("%s/%s-%s", projectName, service.Name, envName))
	}
	parsed, err := docker.ParseContainerImage(image)
	if err != nil {
		return nil, fmt.Errorf("parse docker.image for preview: %w", err)
	}
	plan.Repository = parsed.Repository
	if parsed.Tag == "" {
		parsed.Tag, err = expand(service.GetDocker().GetTag())
		if err != nil {
			return nil, fmt.Errorf("resolve docker.tag: %w", err)
		}
	}
	plan.Tag = parsed.Tag
	registry := environment["AZURE_CONTAINER_REGISTRY_ENDPOINT"]
	if registry == "" {
		registry, err = expand(service.GetDocker().GetRegistry())
		if err != nil {
			return nil, fmt.Errorf("resolve docker.registry: %w", err)
		}
	}
	if registry != "" {
		parsed.Registry = registry
	}
	// Core uses azd-deploy-<timestamp> when no tag is configured. Do not
	// invent a timestamp that would differ from the subsequent deployment.
	if parsed.Tag == "" || parsed.Registry == "" || previewContainsUnknown(parsed.Remote()) {
		return plan, nil
	}
	if !containerref.IsValid(parsed.Remote()) {
		return nil, exterrors.Validation(exterrors.CodeInvalidServiceConfig,
			"the configured build image reference is invalid", "check docker.image, docker.tag, and docker.registry")
	}
	plan.Image = parsed.Remote()
	plan.Known = true
	return plan, nil
}

func previewImageOption(plan *DeployPreviewImage) []agent_yaml.AgentBuildOption {
	if plan.Mode == "code" {
		return nil
	}
	image := plan.Image
	if !plan.Known {
		image = pendingPreviewImage
	}
	return []agent_yaml.AgentBuildOption{agent_yaml.WithImageURL(image)}
}

func previewDefinitionDefaults(definition *agent_yaml.ContainerAgent, code bool) error {
	if code && definition.CodeConfiguration == nil {
		if language := strings.ToLower(strings.TrimSpace(definition.Language)); language != "" && language != "python" {
			return exterrors.Validation(exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("language %q requires an explicit code configuration", language),
				"set code_configuration.runtime and entry_point, or codeConfiguration in azure.yaml")
		}
		definition.CodeConfiguration = &agent_yaml.CodeConfiguration{Runtime: "python_3_13", EntryPoint: "main.py"}
	}
	if definition.Resources == nil {
		definition.Resources = &agent_yaml.ContainerResources{}
	}
	if definition.Resources.Cpu == "" {
		definition.Resources.Cpu = DefaultCpu
	}
	if definition.Resources.Memory == "" {
		definition.Resources.Memory = DefaultMemory
	}
	return nil
}
