// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const defaultRecipeName = "code_rl_with_rle"

// defaultRegistryLoginServer hosts derived per-environment images when no explicit
// image is provided.
const defaultRegistryLoginServer = "devrle.azurecr.io"

// recipeImages maps a recipe name to a default image. An empty value means the image
// repository is derived from the environment name at deploy time.
var recipeImages = map[string]string{
	defaultRecipeName: "",
}

func resolveRecipeImage(recipe string, imageOverride string) (string, error) {
	if imageOverride != "" {
		return imageOverride, nil
	}

	image, ok := recipeImages[recipe]
	if !ok {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Unknown RLE recipe %q.", recipe),
			Code:       "rle_unknown_recipe",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf("Use recipe %s or provide an image in rle.yaml.", defaultRecipeName),
		}
	}

	return image, nil
}
