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

type rleDeployFlags struct {
	dockerfile string
}

type deployAction struct {
	cmd   *cobra.Command
	flags *rleDeployFlags
}

func newDeployCommand() *cobra.Command {
	flags := &rleDeployFlags{}

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Create or update the RLE environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return (&deployAction{cmd: cmd, flags: flags}).Run()
		},
	}

	cmd.Flags().StringVar(&flags.dockerfile, "dockerfile", "",
		"Dockerfile path relative to the current folder. Defaults to Dockerfile at the source root or server/Dockerfile.")
	return cmd
}

func (a *deployAction) Run() error {
	state, initialized, err := resolveDeployState(a.flags)
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
			Message:    "Foundry project endpoint is required for deploy.",
			Code:       "rle_project_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf("Set %s=https://<account>.services.ai.azure.com/api/projects/<project>.", foundryProjectEndpointEnvVar),
		}
	}

	image, err := resolveDeployImage(a.flags, state)
	if err != nil {
		return err
	}
	if !project.IsAcrImageReference(image) {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("RLE deploy image must be an ACR image reference, got %q.", image),
			Code:       "rle_acr_image_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run deploy again.",
		}
	}
	if err := project.BuildRuntimeImage(a.cmd.Context(), a.cmd.OutOrStdout(), a.cmd.ErrOrStderr(), image, project.BuildOptions{
		Source:     ".",
		Dockerfile: a.flags.dockerfile,
	}); err != nil {
		return err
	}
	if err := project.PushImage(a.cmd.Context(), a.cmd.OutOrStdout(), a.cmd.ErrOrStderr(), image); err != nil {
		return err
	}
	projectName, err := projectRouteSegment(state)
	if err != nil {
		return err
	}
	environmentId := firstNonEmpty(state.EnvironmentId, project.Slug(state.Name))
	client := newRleClient(resolveControlPlaneEndpoint())
	request := v1EnvironmentRequest{
		Name:         state.Name,
		AcrImagePath: image,
	}

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
		state.Name,
		image,
	); err != nil {
		return err
	}
	if state.EnvironmentId == "" {
		environment, err = client.createV1Environment(a.cmd.Context(), projectName, request)
	} else {
		environment, err = client.updateV1Environment(a.cmd.Context(), projectName, environmentId, request)
		if isNotFoundError(err) {
			// The recorded environment no longer exists in the target project
			// (e.g. the project changed or the control plane was reset). Recreate it.
			if _, msgErr := fmt.Fprintf(
				a.cmd.OutOrStdout(),
				"Environment '%s' not found in project '%s'; creating a new one.\n",
				environmentId,
				projectName,
			); msgErr != nil {
				return msgErr
			}
			created = true
			environment, err = client.createV1Environment(a.cmd.Context(), projectName, request)
		}
	}
	if err != nil {
		return serviceError(err)
	}
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
		state.Name,
		state.EnvironmentId,
	); err != nil {
		return err
	}
	body, err := json.MarshalIndent(environmentOutput{
		EnvironmentId: environment.Id,
		ProjectId:     environment.ProjectId,
		Name:          environment.Name,
		AcrImage:      environment.AcrImagePath,
		Version:       state.EnvironmentVersion,
		CreatedAt:     environment.CreatedAt,
		UpdatedAt:     environment.UpdatedAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(a.cmd.OutOrStdout(), string(body)); err != nil {
		return err
	}
	return nil
}

func resolveDeployState(flags *rleDeployFlags) (rleState, bool, error) {
	state, err := loadRleState()
	initialized := err == nil
	if err != nil {
		if localErr, ok := errors.AsType[*azdext.LocalError](err); !ok ||
			localErr.Code != "rle_project_not_initialized" {
			return rleState{}, false, err
		}
		state = defaultRleState(defaultSourceName("."))
	}

	state.Name = firstNonEmpty(state.Name, defaultSourceName("."))

	projectEndpoint, err := resolveFoundryProjectEndpoint()
	if err != nil {
		return rleState{}, false, err
	}
	state.ProjectEndpoint = projectEndpoint

	return state, initialized, nil
}

func resolveDeployImage(flags *rleDeployFlags, state rleState) (string, error) {
	registry := strings.Trim(strings.TrimSpace(os.Getenv("AZURE_CONTAINER_REGISTRY_ENDPOINT")), "/")
	if registry == "" {
		return "", &azdext.LocalError{
			Message:    "ACR registry is required for deploy.",
			Code:       "rle_acr_registry_required",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run deploy again.",
		}
	}
	projectName, err := projectRouteSegment(state)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s-%s:latest", registry, project.Slug(projectName), project.Slug(state.Name)), nil
}

type environmentOutput struct {
	EnvironmentId string `json:"environmentId"`
	ProjectId     string `json:"projectId"`
	Name          string `json:"name"`
	AcrImage      string `json:"acrImage"`
	Version       string `json:"version"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}
