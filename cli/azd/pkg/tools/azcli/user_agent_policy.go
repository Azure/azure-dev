package azcli

import (
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type userAgentPolicy struct {
	userAgent string
}

// Policy to ensure the AZD custom user agent is set on all HTTP requests.
func NewUserAgentPolicy(userAgent string) policy.Policy {
	return &userAgentPolicy{
		userAgent: userAgent,
	}
}

// Sets the custom user-agent string on the underlying request
func (p *userAgentPolicy) Do(req *policy.Request) (*http.Response, error) {
	if strings.TrimSpace(p.userAgent) != "" {
		req.Raw().Header.Set("User-Agent", p.userAgent)
	}
	return req.Next()
}
