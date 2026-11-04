// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"os"

	"azure.ai.content-understanding/internal/cmd"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/log"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func main() {
	root := cmd.NewRootCommand()
	log.SetListener(cmd.SDKLogListener(root))
	azdext.Run(root, azdext.WithPreExecute(func(_ context.Context, root *cobra.Command) error {
		return cmd.ValidateInvocation(root, os.Args[1:])
	}))
}
