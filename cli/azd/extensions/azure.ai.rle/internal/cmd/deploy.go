// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleDeployFlags struct {
	projectId  string
	dockerfile string
}

func newDeployCommand() *cobra.Command {
	flags := &rleDeployFlags{}

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Create or update the RLE environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			state, initialized, err := resolveDeployState(flags)
			if err != nil {
				return err
			}
			if !initialized {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "No %s found; using current folder as the RLE source.\n",
					rleStateFile); err != nil {
					return err
				}
			}

			if state.Project == "" {
				return &azdext.LocalError{
					Message:    "RLE project is required for deploy.",
					Code:       "rle_project_required",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Pass --project-id <project-id> when running azd ai rle deploy.",
				}
			}

			controlPlaneEndpoint := resolveControlPlaneEndpoint()
			image, err := resolveDeployImage(flags, state)
			if err != nil {
				return err
			}
			if !isAcrImageReference(image) {
				return &azdext.LocalError{
					Message:    fmt.Sprintf("RLE deploy image must be an ACR image reference, got %q.", image),
					Code:       "rle_acr_image_required",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Set AZURE_CONTAINER_REGISTRY_ENDPOINT=<registry>.azurecr.io, then run deploy again.",
				}
			}
			if err := buildLocalRuntimeImage(cmd, image, dockerBuildOptions{
				source:     ".",
				dockerfile: flags.dockerfile,
			}); err != nil {
				return err
			}
			if err := pushDockerImage(cmd, image); err != nil {
				return err
			}
			environmentId := firstNonEmpty(state.EnvironmentId, slug(state.Name))
			client := newRleClient(controlPlaneEndpoint)
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
				cmd.OutOrStdout(),
				"%s environment '%s' (image=%s) ...\n",
				action,
				state.Name,
				image,
			); err != nil {
				return err
			}
			if state.EnvironmentId == "" {
				environment, err = client.createV1Environment(cmd.Context(), state.Project, request)
			} else {
				environment, err = client.updateV1Environment(cmd.Context(), state.Project, environmentId, request)
				if isNotFoundError(err) {
					// The recorded environment no longer exists in the target project
					// (e.g. the project changed or the control plane was reset). Recreate it.
					if _, msgErr := fmt.Fprintf(
						cmd.OutOrStdout(),
						"Environment '%s' not found in project '%s'; creating a new one.\n",
						environmentId,
						state.Project,
					); msgErr != nil {
						return msgErr
					}
					created = true
					environment, err = client.createV1Environment(cmd.Context(), state.Project, request)
				}
			}
			if err != nil {
				return serviceError(err)
			}
			state.EnvironmentId = environment.Id
			state.EnvironmentVersion = firstNonEmpty(environment.Version, environment.VersionLabel)
			if err := saveRleState(state); err != nil {
				return err
			}

			label := "Created"
			if !created {
				label = "Updated"
			}
			if _, err := fmt.Fprintf(
				cmd.OutOrStdout(),
				"\n%s environment '%s' (%s).\n",
				label,
				state.Name,
				state.EnvironmentId,
			); err != nil {
				return err
			}
			body, err := json.MarshalIndent(environmentOutput{
				EnvironmentId: environment.Id,
				ProjectId:     firstNonEmpty(environment.ProjectId, state.Project),
				Name:          firstNonEmpty(environment.Name, state.Name),
				AcrImage:      firstNonEmpty(environment.AcrImagePath, image),
				Version:       state.EnvironmentVersion,
				CreatedAt:     environment.CreatedAt,
				UpdatedAt:     environment.UpdatedAt,
			}, "", "  ")
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(body)); err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flags.projectId, "project-id", "",
		"RLE project id to deploy into. Required on first deploy; later deploys reuse the saved value.")
	cmd.Flags().StringVar(&flags.dockerfile, "dockerfile", "",
		"Dockerfile path relative to the current folder. Defaults to Dockerfile at the source root or server/Dockerfile.")
	return cmd
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
	state.Project = firstNonEmpty(flags.projectId, state.Project)

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
	return fmt.Sprintf("%s/%s-%s:latest", registry, slug(state.Project), slug(state.Name)), nil
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
