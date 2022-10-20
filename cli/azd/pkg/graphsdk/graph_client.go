package graphsdk

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

type GraphClient struct {
	pipeline runtime.Pipeline
	host     string
}

// Creates a new instance of the Microsoft Graph client
func NewGraphClient(
	credential azcore.TokenCredential,
	options *azcore.ClientOptions,
) (*GraphClient, error) {
	if options == nil {
		options = &azcore.ClientOptions{}
	}

	pipeline := NewPipeline(credential, ServiceConfig, options)

	return &GraphClient{
		pipeline: pipeline,
		host:     ServiceConfig.Endpoint,
	}, nil
}

// Me
func (c *GraphClient) Me() *MeItemRequestBuilder {
	return newMeItemRequestBuilder(c)
}

// Applications

func (c *GraphClient) Applications() *ApplicationListRequestBuilder {
	return NewApplicationsRequestBuilder(c)
}

func (c *GraphClient) ApplicationById(id string) *ApplicationItemRequestBuilder {
	return NewApplicationItemRequestBuilder(c, id)
}

// ServicePrincipals

func (c *GraphClient) ServicePrincipals() *ServicePrincipalListRequestBuilder {
	return NewServicePrincipalListRequestBuilder(c)
}

func (c *GraphClient) ServicePrincipalById(id string) *ServicePrincipalItemRequestBuilder {
	return NewServicePrincipalItemRequestBuilder(c, id)
}
