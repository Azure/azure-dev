// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type dockerBuildOptions struct {
	source     string
	dockerfile string
}

func prepareDockerBuild(opts dockerBuildOptions) (source string, dockerfile string, cleanup func(), err error) {
	source = strings.TrimSpace(opts.source)
	if source == "" {
		source = "."
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return "", "", nil, err
	}

	dockerfile = strings.TrimSpace(opts.dockerfile)
	if dockerfile != "" {
		if !isSafeRelativePath(filepath.Clean(filepath.FromSlash(dockerfile))) {
			return "", "", cleanup, &azdext.LocalError{
				Message:    "Dockerfile path must stay inside the source root.",
				Code:       "rle_dockerfile_path_invalid",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Use a Dockerfile path relative to the source root without '..' segments.",
			}
		}
		if !filepath.IsAbs(dockerfile) {
			dockerfile = filepath.Join(source, dockerfile)
		}
		dockerfile, err = filepath.Abs(dockerfile)
		if err != nil {
			return "", "", cleanup, err
		}
		if !isPathWithinRoot(source, dockerfile) {
			return "", "", cleanup, &azdext.LocalError{
				Message:    "Dockerfile path must stay inside the source root.",
				Code:       "rle_dockerfile_path_invalid",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Use a Dockerfile path relative to the source root without '..' segments.",
			}
		}
		if exists, err := fileExists(dockerfile); err != nil {
			return "", "", cleanup, err
		} else if !exists {
			return "", "", cleanup, missingDockerfileError(source)
		}

		return source, dockerfile, cleanup, nil
	}

	for _, candidate := range []string{
		filepath.Join(source, "Dockerfile"),
		filepath.Join(source, "server", "Dockerfile"),
	} {
		if exists, err := fileExists(candidate); err != nil {
			return "", "", cleanup, err
		} else if exists {
			return source, candidate, cleanup, nil
		}
	}

	return "", "", cleanup, missingDockerfileError(source)
}

func isPathWithinRoot(root string, path string) bool {
	relativePath, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return isSafeRelativePath(filepath.Clean(relativePath))
}

func fileExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !info.IsDir(), nil
}

func missingDockerfileError(source string) error {
	return &azdext.LocalError{
		Message:    fmt.Sprintf("No Dockerfile found under %s.", source),
		Code:       "rle_local_dockerfile_required",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Add Dockerfile at the source root or server/Dockerfile, or pass --dockerfile <path>.",
	}
}

func pushDockerImage(cmd *cobra.Command, image string) error {
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Pushing image %s ...\n", image); err != nil {
		return err
	}
	if err := runDocker(cmd, "push", image); err != nil {
		return &azdext.LocalError{
			Message:    fmt.Sprintf("Failed to push Docker image %q: %v", image, err),
			Code:       "rle_docker_push_failed",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Run az acr login --name <namespace>, then retry.",
		}
	}
	return nil
}

func isAcrImageReference(image string) bool {
	firstSegment, _, _ := strings.Cut(strings.TrimSpace(image), "/")
	return strings.HasSuffix(strings.ToLower(firstSegment), ".azurecr.io")
}
