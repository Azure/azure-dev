// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newDeployCommand() *cobra.Command {
	flags := struct {
		registry  string
		skipBuild bool
	}{}

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
				state.Project = firstNonEmpty(os.Getenv("RLE_PROJECT_NAME"), manifestState.Project, state.Project)
				state.Endpoint = firstNonEmpty(manifestState.Endpoint, state.Endpoint)
				state.Image = firstNonEmpty(manifestState.Image, state.Image)
			}

			image, err := resolveRecipeImage(state.Recipe, state.Image)
			if err != nil {
				return err
			}

			// Host-qualify the image so the control plane / ADC can pull it: the disk-image
			// conversion uses the image reference verbatim as the pull source, so a bare
			// "name:tag" is rewritten to "<registry-login-server>/name:tag".
			loginServer, repoTag := splitImageHost(image)
			if loginServer == "" {
				loginServer = normalizeRegistryLoginServer(flags.registry)
			}
			if loginServer == "" {
				return &azdext.LocalError{
					Message:    "No container registry was specified for the environment image.",
					Code:       "rle_registry_required",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Pass --registry <name> (e.g. devrle) or use a host-qualified image reference.",
				}
			}
			image = loginServer + "/" + repoTag

			environmentId := firstNonEmpty(state.EnvironmentId, slug(state.Name))
			client := newRleClient(resolveControlPlaneEndpoint(state.Endpoint))
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

			// Build the local Dockerfile and push it to the registry before registering, so the
			// environment always points at an image that actually exists in the target ACR.
			registryName := registryShortName(loginServer)
			switch {
			case flags.skipBuild:
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Skipping build (--skip-build); using image '%s'.\n", image); err != nil {
					return err
				}
			case !fileExists("Dockerfile"):
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "No Dockerfile in current directory; skipping build and using image '%s'.\n", image); err != nil {
					return err
				}
			default:
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Building and pushing '%s' to registry '%s' (az acr build) ...\n", repoTag, registryName); err != nil {
					return err
				}
				build := exec.CommandContext(cmd.Context(), "az", "acr", "build", "--registry", registryName, "--image", repoTag, ".")
				build.Stdout = cmd.OutOrStdout()
				build.Stderr = cmd.ErrOrStderr()
				if err := build.Run(); err != nil {
					return &azdext.LocalError{
						Message:    fmt.Sprintf("Failed to build and push image '%s' to registry '%s': %v", repoTag, registryName, err),
						Code:       "rle_acr_build_failed",
						Category:   azdext.LocalErrorCategoryUser,
						Suggestion: "Ensure 'az login' is done, you have push access to the registry, and a valid Dockerfile is present. Use --skip-build to register a prebuilt image.",
					}
				}
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

	cmd.Flags().StringVar(&flags.registry, "registry", "devrle",
		"Container registry (short name or login server) to build and push the environment image into")
	cmd.Flags().BoolVar(&flags.skipBuild, "skip-build", false,
		"Skip building/pushing the image and register the existing image reference as-is")

	return cmd
}

// splitImageHost splits a container image reference into its registry host (login server)
// and the remaining repository:tag. The leading segment is treated as a host only when it
// looks like one (contains '.' or ':' or is "localhost"); otherwise there is no host.
func splitImageHost(image string) (host string, repoTag string) {
	image = strings.TrimSpace(image)
	if slash := strings.IndexByte(image, '/'); slash > 0 {
		first := image[:slash]
		if first == "localhost" || strings.ContainsAny(first, ".:") {
			return first, image[slash+1:]
		}
	}
	return "", image
}

// normalizeRegistryLoginServer turns a registry short name ("devrle") into a login server
// ("devrle.azurecr.io"). A value that already contains a '.' is assumed to be a login server.
func normalizeRegistryLoginServer(registry string) string {
	registry = strings.TrimSpace(registry)
	if registry == "" {
		return ""
	}
	if strings.Contains(registry, ".") {
		return registry
	}
	return registry + ".azurecr.io"
}

// registryShortName returns the ACR short name (the part before ".azurecr.io") for use with
// "az acr build --registry".
func registryShortName(loginServer string) string {
	if host, _, ok := strings.Cut(loginServer, "."); ok {
		return host
	}
	return loginServer
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
