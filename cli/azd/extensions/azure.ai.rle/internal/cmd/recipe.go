// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const defaultRecipeName = "code_rl"

var recipeImages = map[string]string{
	defaultRecipeName: "devrle.azurecr.io/coding_env:latest",
}

func resolveRecipeImage(recipe string, imageOverride string) (string, error) {
	if imageOverride != "" {
		return imageOverride, nil
	}

	image, ok := recipeImages[recipe]
	if !ok {
		return "", &azdext.LocalError{
			Message:  fmt.Sprintf("Unknown RLE recipe %q.", recipe),
			Code:     "rle_unknown_recipe",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf(
				"Use --recipe %s or provide an explicit image with --image.",
				defaultRecipeName,
			),
		}
	}

	return image, nil
}
