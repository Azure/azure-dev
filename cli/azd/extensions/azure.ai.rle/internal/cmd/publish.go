// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"azure.ai.rle/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rlePublishFlags struct {
	dockerfile      string
	versionBump     string
	enableTraces    bool
	enableTracesSet bool
	traceContent    string
	traceContentSet bool
}

type publishAction struct {
	cmd   *cobra.Command
	flags *rlePublishFlags
}

var buildPublishImage = project.BuildRuntimeImage
var pushPublishImage = project.PushImage

func newPublishCommand() *cobra.Command {
	flags := &rlePublishFlags{}
	flags.versionBump = "major"

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Build, push, and create or update the RLE environment",
		Long: "Build and push an RLE image, then create or update its environment. " +
			"Traces are disabled unless --enable-traces is specified. When enabled, traces are sent to the owning " +
			"RLE Foundry project and default to metadata-only content.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&publishAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.dockerfile, "dockerfile", "",
		"Dockerfile path relative to the current folder. Defaults to Dockerfile at the source root or server/Dockerfile.")
	cmd.Flags().StringVar(
		&flags.versionBump,
		"version-bump",
		flags.versionBump,
		"Version bump to apply when creating or updating the environment: major, minor, or patch.",
	)
	cmd.Flags().BoolVar(
		&flags.enableTraces,
		"enable-traces",
		false,
		"Enable traces in the owning RLE Foundry project. Use --enable-traces=false to explicitly disable them.",
	)
	cmd.Flags().StringVar(
		&flags.traceContent,
		"trace-content",
		"",
		"Trace content to collect when traces are enabled: metadata or full. Defaults to metadata.",
	)
	return cmd
}

func (a *publishAction) Run() error {
	a.flags.enableTracesSet = a.cmd.Flags().Changed("enable-traces")
	a.flags.traceContentSet = a.cmd.Flags().Changed("trace-content")
	versionBump, err := normalizeVersionBumpFlag(a.flags.versionBump)
	if err != nil {
		return err
	}
	traceContent, err := normalizeTraceContentFlag(a.flags)
	if err != nil {
		return err
	}
	a.flags.traceContent = traceContent

	state, initialized, err := resolvePublishState()
	if err != nil {
		return err
	}
	if !initialized {
		if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "No %s found; using current folder as the RLE source.\n",
			rleStateFile); err != nil {
			return err
		}
	}

	if state.ProjectEndpoint == "" {
		return &azdext.LocalError{
			Message:  "Foundry project endpoint is required for publish.",
			Code:     "rle_project_required",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf(
				"Set %s=https://<account>.services.ai.azure.com/api/projects/<project>.",
				foundryProjectEndpointEnvVar,
			),
		}
	}

	image, err := resolvePublishImage(state)
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
	if err := buildPublishImage(
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
	if err := pushPublishImage(a.cmd.Context(), a.cmd.OutOrStdout(), a.cmd.ErrOrStderr(), image); err != nil {
		return err
	}
	client, err := createRleClient(state.ProjectEndpoint)
	if err != nil {
		return err
	}
	request := buildEnvironmentCreateRequest(state.EnvironmentName, image, versionBump)
	request.Traces = publishTracesConfiguration(a.flags)

	var environment *environmentResource
	created := state.EnvironmentId == ""
	action := "Creating"
	if !created {
		action = "Updating"
	}

	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"%s environment '%s' (image=%s) ...\n",
		action,
		state.EnvironmentName,
		image,
	); err != nil {
		return err
	}
	environment, err = client.createV1Environment(a.cmd.Context(), request)
	if err != nil {
		return serviceError(err)
	}
	state.EnvironmentName = environment.Name
	state.EnvironmentId = environment.Id
	state.EnvironmentVersion = environment.Version
	if err := saveRleState(state); err != nil {
		return err
	}

	label := "Created"
	if !created {
		label = "Updated"
	}
	if _, err := fmt.Fprintf(
		a.cmd.OutOrStdout(),
		"\n%s environment '%s' (%s).\n",
		label,
		state.EnvironmentName,
		state.EnvironmentId,
	); err != nil {
		return err
	}
	body, err := json.MarshalIndent(environmentOutput{
		EnvironmentId:          environment.Id,
		EnvironmentVersion:     state.EnvironmentVersion,
		EnvironmentName:        environment.Name,
		FoundryProjectEndpoint: state.ProjectEndpoint,
		AcrImage:               environment.AcrImagePath,
		CreatedAt:              environment.CreatedAt,
		UpdatedAt:              environment.UpdatedAt,
		Traces:                 environment.Traces,
	}, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(a.cmd.OutOrStdout(), string(body)); err != nil {
		return err
	}
	if a.flags.enableTracesSet {
		if _, err := fmt.Fprintf(a.cmd.OutOrStdout(), "Traces enabled: %t\n", a.flags.enableTraces); err != nil {
			return err
		}
	}
	if environment.Traces != nil && environment.Traces.ContentMode != "" {
		if _, err := fmt.Fprintf(
			a.cmd.OutOrStdout(),
			"Trace content: %s\n",
			environment.Traces.ContentMode,
		); err != nil {
			return err
		}
	}
	return nil
}

func publishTracesConfiguration(flags *rlePublishFlags) *tracesConfiguration {
	if !flags.enableTracesSet {
		return nil
	}
	return &tracesConfiguration{
		Enabled:     flags.enableTraces,
		ContentMode: flags.traceContent,
	}
}

func normalizeTraceContentFlag(flags *rlePublishFlags) (string, error) {
	if !flags.traceContentSet {
		return "", nil
	}

	contentMode := strings.ToLower(strings.TrimSpace(flags.traceContent))
	switch contentMode {
	case "metadata", "full":
	default:
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Invalid trace content %q.", flags.traceContent),
			Code:       "rle_invalid_trace_content",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use --trace-content metadata or --trace-content full.",
		}
	}
	if !flags.enableTracesSet || !flags.enableTraces {
		return "", &azdext.LocalError{
			Message:    "--trace-content requires --enable-traces=true.",
			Code:       "rle_trace_content_requires_traces",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Add --enable-traces or remove --trace-content.",
		}
	}
	return contentMode, nil
}

func normalizeVersionBumpFlag(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "major":
		return "Major", nil
	case "minor":
		return "Minor", nil
	case "patch":
		return "Patch", nil
	default:
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Invalid version bump %q.", value),
			Code:       "rle_invalid_version_bump",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use --version-bump major, --version-bump minor, or --version-bump patch.",
		}
	}
}

func buildEnvironmentCreateRequest(name string, image string, versionBump string) v1EnvironmentRequest {
	return v1EnvironmentRequest{
		Name:         name,
		AcrImagePath: image,
		VersionBump:  versionBump,
	}
}

func resolvePublishState() (rleState, bool, error) {
	state, err := loadRleState()
	initialized := err == nil
	if err != nil {
		if localErr, ok := errors.AsType[*azdext.LocalError](err); !ok ||
			localErr.Code != "rle_project_not_initialized" {
			return rleState{}, false, err
		}
		state = defaultRleState(defaultSourceName("."))
	}

	state.EnvironmentName = firstNonEmpty(state.EnvironmentName, defaultSourceName("."))

	projectEndpoint, err := resolveFoundryProjectEndpoint()
	if err != nil {
		return rleState{}, false, err
	}
	if projectEndpoint != "" {
		state.ProjectEndpoint = projectEndpoint
	}

	return state, initialized, nil
}

func resolvePublishImage(state rleState) (string, error) {
	registry := strings.Trim(strings.TrimSpace(os.Getenv("AZURE_CONTAINER_REGISTRY_ENDPOINT")), "/")
	if registry == "" {
		return "", &azdext.LocalError{
			Message:    "ACR registry is required for publish.",
			Code:       "rle_acr_registry_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run publish again.",
		}
	}
	projectName, err := projectRouteSegment(state)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%s/%s-%s:latest",
		registry,
		project.Slug(projectName),
		project.Slug(state.EnvironmentName),
	), nil
}

type environmentOutput struct {
	EnvironmentId          string             `json:"environmentId"`
	EnvironmentVersion     string             `json:"environmentVersion"`
	EnvironmentName        string             `json:"environmentName"`
	FoundryProjectEndpoint string             `json:"foundryProjectEndpoint"`
	AcrImage               string             `json:"acrImage"`
	CreatedAt              string             `json:"createdAt"`
	UpdatedAt              string             `json:"updatedAt"`
	Traces                 *environmentTraces `json:"traces,omitempty"`
}
