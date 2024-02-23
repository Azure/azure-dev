package azsdk

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type ClientOptionsBuilderFactory struct {
	defaultTransport policy.Transporter
	defaultUserAgent string
}

func NewClientOptionsBuilderFactory(httpClient httputil.HttpClient, userAgent string) *ClientOptionsBuilderFactory {
	return &ClientOptionsBuilderFactory{
		defaultTransport: httpClient,
		defaultUserAgent: userAgent,
	}
}

func (c *ClientOptionsBuilderFactory) NewClientOptionsBuilder() *ClientOptionsBuilder {
	return NewClientOptionsBuilder().
		WithTransport(c.defaultTransport).
		WithPerCallPolicy(NewUserAgentPolicy(c.defaultUserAgent)).
		WithPerCallPolicy(NewMsCorrelationPolicy())
}
