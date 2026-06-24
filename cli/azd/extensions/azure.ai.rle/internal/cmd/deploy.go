// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

type rleDeployFlags struct {
	project string
}

func newDeployCommand() *cobra.Command {
	flags := &rleDeployFlags{}

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Create or update the RLE environment",
		RunE: func(cmd *cobra.Command, args []string) error {
			state, err := loadRleState()
			if err != nil {
				return err
			}

			manifest, err := loadRleManifest(rleManifestFile)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err == nil {
				manifestState, err := stateFromManifest(manifest)
				if err != nil {
					return err
				}
				state.Name = firstNonEmpty(manifestState.Name, state.Name)
				state.Account = firstNonEmpty(manifestState.Account, state.Account)
				state.Project = firstNonEmpty(manifestState.Project, state.Project)
				state.Endpoint = firstNonEmpty(manifestState.Endpoint, state.Endpoint)
				state.Image = firstNonEmpty(manifestState.Image, state.Image)
			}
			state.Project = firstNonEmpty(flags.project, state.Project)

			image, err := resolveRecipeImage(state.Recipe, state.Image)
			if err != nil {
				return err
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

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Skipping build; using existing image '%s'.\n", image); err != nil {
				return err
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s environment '%s' (image=%s) ...\n", action, state.Name, image); err != nil {
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
			state.EnvironmentVersion = firstNonEmpty(environment.Version, environment.VersionLabel, environment.Manifest.VersionLabel)
			state.InstanceId = ""
			state.InstanceEndpoint = ""
			if err := saveRleState(state); err != nil {
				return err
			}

			label := "Created"
			if !created {
				label = "Updated"
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "\n%s environment '%s' (%s).\n", label, state.Name, state.EnvironmentId); err != nil {
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

	cmd.Flags().StringVar(&flags.project, "project", "", "RLE project name. Defaults to the project saved in .azd-rle.json.")
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
