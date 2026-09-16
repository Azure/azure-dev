// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rlePublishFlags struct {
	dockerfile string
}

type publishAction struct {
	cmd   *cobra.Command
	flags *rlePublishFlags
}

func newPublishCommand() *cobra.Command {
	flags := &rlePublishFlags{}

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Build, push, and publish the RLE release declared in rle.toml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&publishAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.dockerfile, "dockerfile", "",
		"Dockerfile path relative to the current folder. Defaults to Dockerfile at the source root or server/Dockerfile.")
	return cmd
}

func (a *publishAction) Run() error {
	config, projectEndpoint, client, creating, err := resolvePublishTarget(a.cmd.Context())
	if err != nil {
		return err
	}

	image, err := resolvePublishImage(config.Rle.Name, config.Rle.Version, projectEndpoint)
	if err != nil {
		return err
	}
	if !project.IsAcrImageReference(image) {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("RLE publish image must be an ACR image reference, got %q.", image),
			Code:       "rle_acr_image_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run publish again.",
		}
	}
	if err := project.BuildRuntimeImage(
		a.cmd.Context(),
		a.cmd.OutOrStdout(),
		a.cmd.ErrOrStderr(),
		image,
		project.BuildOptions{
			Source:     ".",
			Dockerfile: a.flags.dockerfile,
		},
	); err != nil {
		return err
	}
	if err := project.PushImage(a.cmd.Context(), a.cmd.OutOrStdout(), a.cmd.ErrOrStderr(), image); err != nil {
		return err
	}
	request := buildEnvironmentCreateRequest(config, image)

	action := "Publishing"
	if creating {
		action = "Creating"
	}
	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"%s environment '%s' version %s (image=%s) ...\n",
		action,
		config.Rle.Name,
		config.Rle.Version,
		image,
	); err != nil {
		return err
	}
	environment, err := client.createV1Environment(a.cmd.Context(), request)
	if err != nil {
		return serviceError(err)
	}
	if err := verifyPublishedEnvironment(config, environment); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"\nPublished environment '%s' version %s (%s).\n",
		environment.Name,
		environment.Version,
		environment.Id,
	); err != nil {
		return err
	}
	body, err := json.MarshalIndent(environmentOutput{
		EnvironmentId:          environment.Id,
		EnvironmentVersion:     environment.Version,
		EnvironmentName:        environment.Name,
		FoundryProjectEndpoint: projectEndpoint,
		AcrImage:               environment.AcrImagePath,
		Type:                   environment.Type,
		Subtype:                environment.Subtype,
		AgentName:              environment.AgentName,
		AgentVersion:           environment.AgentVersion,
		BaseURL:                environment.BaseURL,
		SchemaVersion:          environment.SchemaVersion,
		Defaults:               environment.Defaults,
		Metadata:               environment.Metadata,
		CreatedAt:              environment.CreatedAt,
		UpdatedAt:              environment.UpdatedAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(a.cmd.OutOrStdout(), string(body)); err != nil {
		return err
	}
	return nil
}

func resolvePublishTarget(
	ctx context.Context,
) (project.RleConfig, string, *rleClient, bool, error) {
	config, err := project.LoadRleConfig(".")
	if err != nil {
		return project.RleConfig{}, "", nil, false, err
	}

	projectEndpoint, err := resolveFoundryProjectEndpoint()
	if err != nil {
		return project.RleConfig{}, "", nil, false, err
	}
	if projectEndpoint == "" {
		return project.RleConfig{}, "", nil, false, &azdext.LocalError{
			Message:  "Foundry project endpoint is required for publish.",
			Code:     "rle_project_required",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf(
				"Set %s=https://<account>.services.ai.azure.com/api/projects/<project>.",
				foundryProjectEndpointEnvVar,
			),
		}
	}

	client, err := createRleClient(projectEndpoint)
	if err != nil {
		return project.RleConfig{}, "", nil, false, err
	}
	_, err = client.getEnvironmentByName(ctx, config.Rle.Name)
	creating := false
	switch {
	case err == nil:
	case isRleNotFound(err):
		creating = true
		if err := project.ValidateInitialRleVersion(config.Rle.Version); err != nil {
			return project.RleConfig{}, "", nil, false, err
		}
	default:
		return project.RleConfig{}, "", nil, false, serviceError(err)
	}

	return config, projectEndpoint, client, creating, nil
}

func buildEnvironmentCreateRequest(
	config project.RleConfig,
	image string,
) v1EnvironmentRequest {
	return v1EnvironmentRequest{
		Name:          config.Rle.Name,
		AcrImagePath:  image,
		Version:       config.Rle.Version,
		Type:          string(config.Rle.Type),
		Subtype:       string(config.Rle.Subtype),
		AgentName:     config.Rle.AgentName,
		AgentVersion:  config.Rle.AgentVersion,
		BaseURL:       config.Rle.BaseURL,
		SchemaVersion: config.SchemaVersion,
		Defaults:      config.Defaults,
		Metadata:      config.Metadata,
	}
}

func verifyPublishedEnvironment(config project.RleConfig, environment *environmentResource) error {
	manifest := config.Rle
	if environment == nil {
		return publishedEnvironmentMismatchError(
			"RLE service did not return the published environment.",
			"Check the RLE service response, then retry.",
		)
	}
	if environment.Name != manifest.Name || environment.Version != manifest.Version {
		return publishedEnvironmentMismatchError(
			fmt.Sprintf(
				"RLE service returned %s/%s, but rle.toml declares %s/%s.",
				environment.Name,
				environment.Version,
				manifest.Name,
				manifest.Version,
			),
			"Resolve the concurrent or deleted-version conflict, then publish the version declared in rle.toml.",
		)
	}
	if environment.Type != string(manifest.Type) || environment.Subtype != string(manifest.Subtype) {
		return publishedEnvironmentMismatchError(
			fmt.Sprintf(
				"RLE service returned type %s/%s, but rle.toml declares %s/%s.",
				environment.Type,
				environment.Subtype,
				manifest.Type,
				manifest.Subtype,
			),
			"Check the RLE service response and retry.",
		)
	}
	if manifest.AgentName != nil && environment.AgentName != *manifest.AgentName {
		return publishedEnvironmentMismatchError(
			"RLE service returned a different HostedAgent name than rle.toml.",
			"Check the RLE service response and retry.",
		)
	}
	if manifest.AgentVersion != nil && environment.AgentVersion != *manifest.AgentVersion {
		return publishedEnvironmentMismatchError(
			"RLE service returned a different HostedAgent version than rle.toml.",
			"Check the RLE service response and retry.",
		)
	}
	if manifest.BaseURL != nil && environment.BaseURL != *manifest.BaseURL {
		return publishedEnvironmentMismatchError(
			"RLE service returned a different BYOH baseUrl than rle.toml.",
			"Check the RLE service response and retry.",
		)
	}
	if !reflect.DeepEqual(config.SchemaVersion, environment.SchemaVersion) {
		return publishedEnvironmentMismatchError(
			"RLE service returned a different metadata schema version than rle.toml.",
			"Check the RLE service response and retry.",
		)
	}
	if !reflect.DeepEqual(config.Defaults, environment.Defaults) {
		return publishedEnvironmentMismatchError(
			"RLE service returned different reusable defaults than rle.toml.",
			"Check the RLE service response and retry.",
		)
	}
	if !reflect.DeepEqual(config.Metadata, environment.Metadata) {
		return publishedEnvironmentMismatchError(
			"RLE service returned different metadata than rle.toml.",
			"Check the RLE service response and retry.",
		)
	}
	return nil
}

func publishedEnvironmentMismatchError(message string, suggestion string) error {
	return &azdext.LocalError{
		Message:    message,
		Code:       "rle_published_environment_mismatch",
		Category:   azdext.LocalErrorCategoryInternal,
		Suggestion: suggestion,
	}
}

func resolvePublishImage(environmentName string, version string, projectEndpoint string) (string, error) {
	registry := strings.Trim(strings.TrimSpace(os.Getenv("AZURE_CONTAINER_REGISTRY_ENDPOINT")), "/")
	if registry == "" {
		return "", &azdext.LocalError{
			Message:    "ACR registry is required for publish.",
			Code:       "rle_acr_registry_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run publish again.",
		}
	}
	projectName, err := projectRouteSegment(projectEndpoint)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%s/%s-%s:%s",
		registry,
		project.Slug(projectName),
		project.Slug(environmentName),
		version,
	), nil
}

type environmentOutput struct {
	EnvironmentId          string                          `json:"environmentId"`
	EnvironmentVersion     string                          `json:"environmentVersion"`
	EnvironmentName        string                          `json:"environmentName"`
	FoundryProjectEndpoint string                          `json:"foundryProjectEndpoint"`
	AcrImage               string                          `json:"acrImage"`
	Type                   string                          `json:"type"`
	Subtype                string                          `json:"subtype"`
	AgentName              string                          `json:"agentName,omitempty"`
	AgentVersion           string                          `json:"agentVersion,omitempty"`
	BaseURL                string                          `json:"baseUrl,omitempty"`
	SchemaVersion          *string                         `json:"schemaVersion,omitempty"`
	Defaults               *project.RleEnvironmentDefaults `json:"defaults,omitempty"`
	Metadata               map[string]string               `json:"metadata,omitempty"`
	CreatedAt              string                          `json:"createdAt"`
	UpdatedAt              string                          `json:"updatedAt"`
}
