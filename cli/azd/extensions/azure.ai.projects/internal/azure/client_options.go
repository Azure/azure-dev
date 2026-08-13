// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package azure configures Azure SDK clients for this extension.
package azure

import (
	"fmt"
	"net/http"

	"azure.ai.projects/internal/pkg/recordproxy"
	"azure.ai.projects/internal/version"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
)

// NewArmClientOptions returns the extension's ARM client options.
func NewArmClientOptions() *arm.ClientOptions {
	userAgent := fmt.Sprintf(
		"azd-ext-azure-ai-projects/%s",
		version.Version,
	)
	options := &arm.ClientOptions{
		ClientOptions: policy.ClientOptions{
			Logging: policy.LogOptions{
				AllowedHeaders: []string{
					azsdk.MsCorrelationIdHeader,
				},
			},
			PerCallPolicies: []policy.Policy{
				azsdk.NewMsCorrelationPolicy(),
				azsdk.NewUserAgentPolicy(userAgent),
			},
		},
	}

	if recordproxy.Transport != nil {
		options.ClientOptions.Transport = &http.Client{
			Transport: recordproxy.Transport,
		}
	}

	return options
}
