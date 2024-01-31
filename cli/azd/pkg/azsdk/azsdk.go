package azsdk

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type BasicClientOptions struct {
	transport policy.Transporter
}

// TODO: RENAME
type ClientOptionsBuilderFactory struct {
	defaultTransport policy.Transporter
	defaultUserAgent string
}

func NewClientOptionsBuilderFactory(
	httpClient httputil.HttpClient,
	userAgent string,
) *ClientOptionsBuilderFactory {
	return &ClientOptionsBuilderFactory{
		defaultTransport: httpClient,
		defaultUserAgent: userAgent,
	}
}

// Returns a new ClientOptionsBuilder with the default transport and user agent
func (c *ClientOptionsBuilderFactory) ClientOptionsBuilder() *ClientOptionsBuilder {
	return NewClientOptionsBuilder().
		WithTransport(c.defaultTransport).
		SetUserAgent(c.defaultUserAgent)
}

func DefaultClientOptionsBuilder(
	ctx context.Context,
	httpClient httputil.HttpClient,
	userAgent string) *ClientOptionsBuilder {
	return NewClientOptionsBuilder().
		WithTransport(httpClient).
		WithPerCallPolicy(NewUserAgentPolicy(userAgent)).
		WithPerCallPolicy(NewMsCorrelationPolicy(ctx))
}
