package mocks

import (
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// This is a mock of the UserAgentPolicy used in the azsdk package. Importing
// the azsdk package would cause a circular dependency so we have to mock it.

const userAgentHeaderName = "User-Agent"

type mockUserAgentPolicy struct {
	userAgent string
}

func NewMockUserAgentPolicy(userAgent string) *mockUserAgentPolicy {
	return &mockUserAgentPolicy{userAgent: userAgent}
}

func (p *mockUserAgentPolicy) Do(req *policy.Request) (*http.Response, error) {
	if strings.TrimSpace(p.userAgent) != "" {
		rawRequest := req.Raw()
		userAgent, ok := rawRequest.Header[userAgentHeaderName]
		if !ok {
			userAgent = []string{}
		}
		userAgent = append(userAgent, p.userAgent)
		rawRequest.Header.Set(userAgentHeaderName, strings.Join(userAgent, ","))
	}

	return req.Next()
}
