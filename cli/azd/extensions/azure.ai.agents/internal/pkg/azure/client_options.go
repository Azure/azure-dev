// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"azureaiagent/internal/version"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// NewArmClientOptions creates a new arm.ClientOptions with standard policies for Azure SDK clients.
// This includes correlation headers, user agent, and logging configuration.
func NewArmClientOptions() *arm.ClientOptions {
	userAgent := fmt.Sprintf("azd-ext-azure-ai-agents/%s", version.Version)

	return &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Logging: policy.LogOptions{
				AllowedHeaders: []string{azsdk.MsCorrelationIdHeader},
			},
			PerCallPolicies: []policy.Policy{
				azsdk.NewMsCorrelationPolicy(),
				azsdk.NewUserAgentPolicy(userAgent),
			},
		},
	}
}
