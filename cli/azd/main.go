// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"os"

	"github.com/azure/azure-dev/cli/azd/cmd"
)

func main() {
	err := cmd.Execute(os.Args[1:])
	if err != nil {
		os.Exit(1)
	}
}
