// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package devcentersdk

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const (
	apiVersionName    = "api-version"
	defaultApiVersion = "2024-02-01"
)

type apiVersionPolicy struct {
	apiVersion string
}

// NewApiVersionPolicy returns a policy that sets the `api-version` query
// parameter on all outgoing requests. If apiVersion is nil, the default
// api-version is used.
func NewApiVersionPolicy(apiVersion *string) policy.Policy {
	if apiVersion == nil {
		apiVersion = new(defaultApiVersion)
	}

	return &apiVersionPolicy{
		apiVersion: *apiVersion,
	}
}

// Sets the api-version query parameter on the underlying request
func (p *apiVersionPolicy) Do(req *policy.Request) (*http.Response, error) {
	rawRequest := req.Raw()
	queryString := rawRequest.URL.Query()
	queryString.Set(apiVersionName, p.apiVersion)
	rawRequest.URL.RawQuery = queryString.Encode()

	return req.Next()
}
