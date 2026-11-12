// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/test/azurecleanup"
)

func main() {
	var subscriptionID string
	var environmentName string
	flag.StringVar(&subscriptionID, "subscription", "", "Azure subscription ID")
	flag.StringVar(&environmentName, "environment", "", "azd environment name")
	flag.Parse()

	if subscriptionID == "" || environmentName == "" {
		fmt.Fprintln(os.Stderr, "--subscription and --environment are required")
		os.Exit(2)
	}

	credential, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create Azure CLI credential: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := azurecleanup.CleanupSubscriptionDeployments(
		ctx,
		credential,
		subscriptionID,
		environmentName,
	); err != nil {
		fmt.Fprintf(os.Stderr, "cleanup subscription deployments: %v\n", err)
		os.Exit(1)
	}
}
