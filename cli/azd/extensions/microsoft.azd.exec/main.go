// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"errors"

	"microsoft.azd.exec/internal/cmd"
	"microsoft.azd.exec/internal/executor"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func main() {
	azdext.Run(
		cmd.NewRootCommand(),
		azdext.WithExitCode(func(err error) (int, bool) {
			if execErr, ok := errors.AsType[*executor.ExecutionError](err); ok {
				return execErr.ExitCode, true
			}
			return 0, false
		}),
	)
}
