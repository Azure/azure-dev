// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const defaultRecipeName = "code_rl_with_rle"

func resolveRecipeImage(recipe string, imageOverride string) (string, error) {
	if imageOverride != "" {
		return imageOverride, nil
	}

	if recipe != defaultRecipeName {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Unknown RLE recipe %q.", recipe),
			Code:       "rle_unknown_recipe",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf("Use recipe %s or provide an image in rle.yaml.", defaultRecipeName),
		}
	}

	return "", &azdext.LocalError{
		Message:    "RLE environment image is required.",
		Code:       "rle_image_required",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Set RLE_ACR_IMAGE, then run azd ai rle deploy again.",
	}
}
