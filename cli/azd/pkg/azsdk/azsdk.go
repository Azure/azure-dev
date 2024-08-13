package azsdk

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
)

type ClientOptionsBuilderFactory struct {
	defaultTransport policy.Transporter
	defaultUserAgent string
	cloud            *cloud.Cloud
}

func NewClientOptionsBuilderFactory(
	transport policy.Transporter,
	userAgent string,
	cloud *cloud.Cloud,
) *ClientOptionsBuilderFactory {
	return &ClientOptionsBuilderFactory{
		defaultTransport: transport,
		defaultUserAgent: userAgent,
		cloud:            cloud,
	}
}

func (c *ClientOptionsBuilderFactory) NewClientOptionsBuilder() *ClientOptionsBuilder {
	return NewClientOptionsBuilder().
		WithTransport(c.defaultTransport).
		WithPerCallPolicy(NewUserAgentPolicy(c.defaultUserAgent)).
		WithCloud(c.cloud.Configuration)
}
