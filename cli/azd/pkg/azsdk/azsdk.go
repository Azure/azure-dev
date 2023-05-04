package azsdk

import "github.com/azure/azure-dev/cli/azd/pkg/httputil"

func DefaultClientOptionsBuilder(httpClient httputil.HttpClient, userAgent string) *ClientOptionsBuilder {
	return NewClientOptionsBuilder().
		WithTransport(httpClient).
		WithPerCallPolicy(NewUserAgentPolicy(userAgent))
}
