// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

const (
	azureResourceAddItemID = "azure-resource"
	invalidAddItemCode     = "invalid_add_item"
	addRequiresPromptCode  = "add_requires_prompt"
)

func configureAddProvider(host *azdext.ExtensionHost, extCtx *azdext.ExtensionContext, writer io.Writer) {
	host.WithBetaAddProvider(
		[]*v1beta.AddItem{{Id: azureResourceAddItemID, DisplayName: "Azure resource or service"}},
		newAddProviderHandler(host.Client(), extCtx, writer),
	)
}

func newAddProviderHandler(
	client *azdext.AzdClient, extCtx *azdext.ExtensionContext, writer io.Writer,
) func(context.Context, string) error {
	return func(ctx context.Context, itemID string) error {
		if itemID != azureResourceAddItemID {
			return &azdext.LocalError{
				Category: azdext.LocalErrorCategoryValidation, Code: invalidAddItemCode,
				Message:    "unrecognized Azure Resources add item",
				Suggestion: "Run azd add and select Azure resource or service.",
			}
		}
		if extCtx.NoPrompt {
			return &azdext.LocalError{
				Category: azdext.LocalErrorCategoryValidation, Code: addRequiresPromptCode,
				Message:    "adding an Azure resource or service requires interactive input",
				Suggestion: "Run azd add without --no-prompt.",
			}
		}
		projectDir, err := azdext.GetProjectDir()
		if err != nil {
			return err
		}
		return runAddWizard(ctx, client, writer, projectDir, extCtx.Environment)
	}
}
