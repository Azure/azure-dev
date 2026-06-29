// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type rleDeployFlags struct {
	projectId string
}

func newDeployCommand() *cobra.Command {
	flags := &rleDeployFlags{}

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Create or update the RLE environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load the persisted session state. When .azd-rle.json is absent we treat this as a
			// first-time bootstrap and initialize the state in-place (it is persisted on success),
			// so `deploy` can run without a prior `init` as long as the folder has an rle.yaml.
			state, err := loadRleState()
			initialized := err == nil
			if err != nil {
				if localErr, ok := errors.AsType[*azdext.LocalError](err); !ok ||
					localErr.Code != "rle_project_not_initialized" {
					return err
				}
			}

			manifest, err := loadRleManifest(rleManifestFile)
			manifestExists := err == nil
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}

			if !initialized && !manifestExists {
				return &azdext.LocalError{
					Message:  "RLE session has not been initialized.",
					Code:     "rle_project_not_initialized",
					Category: azdext.LocalErrorCategoryUser,
					Suggestion: "Run azd ai rle init first, or add an " + rleManifestFile +
						" manifest to this folder, then re-run deploy.",
				}
			}

			if !initialized {
				state = defaultRleState("")
				if _, err := fmt.Fprintf(
					cmd.OutOrStdout(),
					"No %s found; initializing a new RLE session from %s.\n",
					rleStateFile,
					rleManifestFile,
				); err != nil {
					return err
				}
			}

			if manifestExists {
				manifestState, err := stateFromManifest(manifest)
				if err != nil {
					return err
				}
				state.Name = firstNonEmpty(manifestState.Name, state.Name)
				state.LocalImage = manifestState.LocalImage
				state.Image = manifestState.Image
			}
			state.Project = firstNonEmpty(flags.projectId, state.Project)
			if state.Project == "" {
				return &azdext.LocalError{
					Message:    "RLE project is required for deploy.",
					Code:       "rle_project_required",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Pass --project-id <project-id> when running azd ai rle deploy.",
				}
			}

			image := state.Image
			if image == "" {
				return &azdext.LocalError{
					Message:    "RLE environment image is required for deploy.",
					Code:       "rle_image_required",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Set template.environment.image in rle.yaml, then rerun deploy.",
				}
			}
			environmentId := firstNonEmpty(state.EnvironmentId, slug(state.Name))
			client := newRleClient(resolveControlPlaneEndpoint(""))
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
				Id:           environment.Id,
				ProjectId:    firstNonEmpty(environment.ProjectId, state.Project),
				Name:         firstNonEmpty(environment.Name, state.Name),
				AcrImagePath: firstNonEmpty(environment.AcrImagePath, image),
				Version:      state.EnvironmentVersion,
				CreatedAtUtc: environment.CreatedAtUtc,
				UpdatedAtUtc: environment.UpdatedAtUtc,
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
	return cmd
}

type environmentOutput struct {
	Id           string `json:"id"`
	ProjectId    string `json:"projectId"`
	Name         string `json:"name"`
	AcrImagePath string `json:"acrImagePath"`
	Version      string `json:"version"`
	CreatedAtUtc string `json:"createdAtUtc"`
	UpdatedAtUtc string `json:"updatedAtUtc"`
}
