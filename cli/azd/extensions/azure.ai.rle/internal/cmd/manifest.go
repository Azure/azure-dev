// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"gopkg.in/yaml.v3"
)

type rleManifest struct {
	Name        string                 `yaml:"name"`
	Account     string                 `yaml:"account"`
	Project     string                 `yaml:"project"`
	Endpoint    string                 `yaml:"endpoint"`
	Image       string                 `yaml:"image"`
	Environment rleManifestEnvironment `yaml:"environment"`
}

type rleManifestEnvironment struct {
	Image string `yaml:"image"`
}

func loadRleManifest(path string) (rleManifest, error) {
	data, err := os.ReadFile(path) //nolint:gosec // Manifest path is provided by the user.
	if err != nil {
		return rleManifest{}, err
	}

	expanded, err := expandManifestEnv(string(data))
	if err != nil {
		return rleManifest{}, err
	}

	var manifest rleManifest
	if err := yaml.Unmarshal([]byte(expanded), &manifest); err != nil {
		return rleManifest{}, err
	}

	return manifest, nil
}

func expandManifestEnv(content string) (string, error) {
	missing := map[string]struct{}{}
	expanded := os.Expand(content, func(name string) string {
		value, ok := os.LookupEnv(name)
		if !ok {
			missing[name] = struct{}{}
		}
		return value
	})

	if len(missing) == 0 {
		return expanded, nil
	}

	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	slices.Sort(names)

	return "", &azdext.LocalError{
		Message:  fmt.Sprintf("RLE manifest references unset environment variable(s): %s.", strings.Join(names, ", ")),
		Code:     "rle_manifest_env_missing",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf(
			"Set %s, then run azd ai rle init again.",
			strings.Join(names, ", "),
		),
	}
}

func stateFromManifest(manifest rleManifest) (rleState, error) {
	state := defaultRleState(firstNonEmpty(manifest.Name, "code_rl"), defaultRecipeName)
	state.Account = firstNonEmpty(manifest.Account, defaultAccountName)
	state.Project = firstNonEmpty(manifest.Project, defaultProjectName)
	state.Endpoint = manifest.Endpoint

	image, err := resolveRecipeImage(state.Recipe, firstNonEmpty(manifest.Image, manifest.Environment.Image))
	if err != nil {
		return rleState{}, err
	}
	state.Image = image

	return state, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
