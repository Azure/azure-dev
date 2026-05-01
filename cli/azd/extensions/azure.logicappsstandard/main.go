// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"

	"azurelogicappsstandard/internal/cmd"
)

func main() {
	azdext.Run(cmd.NewRootCommand())
}
