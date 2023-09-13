package devcentersdk

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type DevCenterClient interface {
	DevCenters() *DevCenterListRequestBuilder
	DevCenterByEndpoint(endpoint string) *DevCenterItemRequestBuilder
}

type devCenterClient struct {
	credential azcore.TokenCredential
	options    *azcore.ClientOptions
	pipeline   runtime.Pipeline
}

func NewDevCenterClient(
	credential azcore.TokenCredential,
	options *azcore.ClientOptions,
) (DevCenterClient, error) {
	if options == nil {
		options = &azcore.ClientOptions{}
	}

	options.PerCallPolicies = append(options.PerCallPolicies, NewApiVersionPolicy(nil))
	pipeline := NewPipeline(credential, ServiceConfig, options)

	return &devCenterClient{
		pipeline:   pipeline,
		credential: credential,
		options:    options,
	}, nil
}

func (c *devCenterClient) DevCenters() *DevCenterListRequestBuilder {
	return NewDevCenterListRequestBuilder(c)
}

func (c *devCenterClient) DevCenterByEndpoint(id string) *DevCenterItemRequestBuilder {
	return NewDevCenterItemRequestBuilder(c, id)
}
