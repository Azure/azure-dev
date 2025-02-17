// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/extensions/azd.internal.registry/internal/cmd"
)

func main() {
	ctx := context.Background()
	rootCmd := cmd.NewRootCommand()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
