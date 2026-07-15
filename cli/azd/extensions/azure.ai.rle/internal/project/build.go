// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type BuildOptions struct {
	Source     string
	Dockerfile string
}

func PrepareDockerBuild(opts BuildOptions) (source string, dockerfile string, cleanup func(), err error) {
	source = strings.TrimSpace(opts.Source)
	if source == "" {
		source = "."
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return "", "", nil, err
	}

	dockerfile = strings.TrimSpace(opts.Dockerfile)
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

func IsAcrImageReference(image string) bool {
	firstSegment, _, _ := strings.Cut(strings.TrimSpace(image), "/")
	return strings.HasSuffix(strings.ToLower(firstSegment), ".azurecr.io")
}

func isPathWithinRoot(root string, path string) bool {
	relativePath, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return isSafeRelativePath(filepath.Clean(relativePath))
}

func isSafeRelativePath(path string) bool {
	return path != ".." &&
		!filepath.IsAbs(path) &&
		!strings.HasPrefix(path, ".."+string(os.PathSeparator))
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
