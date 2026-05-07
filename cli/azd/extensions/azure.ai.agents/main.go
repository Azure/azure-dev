// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"azureaiagent/internal"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func main() {
	azdext.Run(internal.NewRootCommand())
}
