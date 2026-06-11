package main

import (
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.review-fixture/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func main() {
	azdext.Run(cmd.NewRootCommand())
}
