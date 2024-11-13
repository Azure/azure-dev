package devcentersdk

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
)

const (
	apiVersionName    = "api-version"
	defaultApiVersion = "2024-02-01"
)

type apiVersionPolicy struct {
	apiVersion string
}

// Policy to ensure the AZD custom user agent is set on all HTTP requests.
func NewApiVersionPolicy(apiVersion *string) policy.Policy {
	if apiVersion == nil {
		apiVersion = to.Ptr(defaultApiVersion)
	}

	return &apiVersionPolicy{
		apiVersion: *apiVersion,
	}
}

// Sets the custom user-agent string on the underlying request
func (p *apiVersionPolicy) Do(req *policy.Request) (*http.Response, error) {
	rawRequest := req.Raw()
	queryString := rawRequest.URL.Query()
	queryString.Set(apiVersionName, defaultApiVersion)
	rawRequest.URL.RawQuery = queryString.Encode()

	return req.Next()
}
