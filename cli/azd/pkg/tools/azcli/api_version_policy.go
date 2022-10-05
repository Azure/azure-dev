package azcli

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type apiVersionPolicy struct {
	apiVersion *string
}

// Policy to ensure the specified api version is set on all HTTP requests.
func NewApiVersionPolicy(apiVersion *string) policy.Policy {
	return &apiVersionPolicy{
		apiVersion: apiVersion,
	}
}

// Sets the specified api-version query param on the underlying request
func (p *apiVersionPolicy) Do(req *policy.Request) (*http.Response, error) {
	if p.apiVersion != nil {
		url := req.Raw().URL
		query := url.Query()
		query.Set("api-version", *p.apiVersion)
		url.RawQuery = query.Encode()
	}

	return req.Next()
}
