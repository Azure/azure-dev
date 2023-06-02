// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:generate goversioninfo

package main

import (
	"os"

	"github.com/azure/azure-dev/cli/azd/cmd"
)

func main() {
	err := cmd.ExecuteMain()
	if err != nil {
		os.Exit(1)
	}
}
