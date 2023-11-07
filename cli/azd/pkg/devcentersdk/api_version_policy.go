package devcentersdk

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

const (
	apiVersionName    = "api-version"
	defaultApiVersion = "2023-04-01"
)

type apiVersionPolicy struct {
	apiVersion string
}

// Policy to ensure the AZD custom user agent is set on all HTTP requests.
func NewApiVersionPolicy(apiVersion *string) policy.Policy {
	if apiVersion == nil {
		apiVersion = convert.RefOf(defaultApiVersion)
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
