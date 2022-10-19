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

	pipeline := NewPipeline(credential, serviceConfig, options)

	return &GraphClient{
		pipeline: pipeline,
		host:     serviceConfig.Endpoint,
	}, nil
}

// Me
func (c *GraphClient) Me() *MeItemRequestBuilder {
	return newMeItemRequestBuilder(c)
}

// Applications

func (c *GraphClient) Applications() *ApplicationListRequestBuilder {
	return newApplicationsRequestBuilder(c)
}

func (c *GraphClient) ApplicationById(id string) *ApplicationItemRequestBuilder {
	return newApplicationItemRequestBuilder(c, id)
}

// ServicePrincipals

func (c *GraphClient) ServicePrincipals() *ServicePrincipalListRequestBuilder {
	return newServicePrincipalListRequestBuilder(c)
}

func (c *GraphClient) ServicePrincipalById(id string) *ServicePrincipalItemRequestBuilder {
	return newServicePrincipalItemRequestBuilder(c, id)
}
