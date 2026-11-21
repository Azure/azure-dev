// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	_ "embed"
	"strings"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.emulators/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

//go:embed version.txt
var version string

func main() {
	azdext.Run(cmd.NewRootCommand(strings.TrimSpace(version)))
}
