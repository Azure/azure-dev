package devcentersdk

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type DevCenterClient interface {
	DevCenters() *DevCenterListRequestBuilder
	DevCenterByEndpoint(endpoint string) *DevCenterItemRequestBuilder
	DevCenterByName(name string) *DevCenterItemRequestBuilder
}

type devCenterClient struct {
	credential azcore.TokenCredential
	options    *azcore.ClientOptions
	pipeline   runtime.Pipeline
	devCenter  *DevCenter
	endpoint   string
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

func (c *devCenterClient) DevCenterByEndpoint(endpoint string) *DevCenterItemRequestBuilder {
	return NewDevCenterItemRequestBuilder(c, &DevCenter{ServiceUri: endpoint})
}

func (c *devCenterClient) DevCenterByName(name string) *DevCenterItemRequestBuilder {
	return NewDevCenterItemRequestBuilder(c, &DevCenter{Name: name})
}

func (c *devCenterClient) host(ctx context.Context) (string, error) {
	if c.endpoint != "" {
		return c.endpoint, nil
	}

	devCenterList, err := c.DevCenters().Get(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get dev center list: %w", err)
	}

	index := slices.IndexFunc(devCenterList.Value, func(devCenter *DevCenter) bool {
		if c.devCenter.ServiceUri != "" {
			return c.devCenter.ServiceUri == devCenter.ServiceUri
		} else if c.devCenter.Name != "" {
			return c.devCenter.Name == devCenter.Name
		}

		return false
	})

	if index < 0 {
		return "", errors.New("failed to find dev center")
	}

	c.endpoint = devCenterList.Value[index].ServiceUri

	return c.endpoint, nil
}

func (c *devCenterClient) createRequest(
	ctx context.Context,
	httpMethod string,
	path string,
) (*policy.Request, error) {
	host, err := c.host(ctx)
	if err != nil {
		return nil, err
	}

	req, err := runtime.NewRequest(ctx, httpMethod, fmt.Sprintf("%s/%s", host, path))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	return req, nil
}
