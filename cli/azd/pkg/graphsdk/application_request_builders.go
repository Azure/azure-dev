package graphsdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type ApplicationListRequestBuilder struct {
	*EntityListRequestBuilder[ApplicationListRequestBuilder]
}

func NewApplicationsRequestBuilder(client *GraphClient) *ApplicationListRequestBuilder {
	builder := &ApplicationListRequestBuilder{}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, client)

	return builder
}

// Gets a list of applications that the current logged in user has access to.
func (c *ApplicationListRequestBuilder) Get(ctx context.Context) (*ApplicationListResponse, error) {
	req, err := c.createRequest(ctx, http.MethodGet, fmt.Sprintf("%s/applications", c.client.host))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, httputil.HandleRequestError(res, err)
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[ApplicationListResponse](res)
}

func (c *ApplicationListRequestBuilder) Post(ctx context.Context, application *Application) (*Application, error) {
	req, err := c.createRequest(ctx, http.MethodPost, fmt.Sprintf("%s/applications", c.client.host))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	err = SetHttpRequestBody(req, application)
	if err != nil {
		return nil, err
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, httputil.HandleRequestError(res, err)
	}

	if !runtime.HasStatusCode(res, http.StatusCreated) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[Application](res)
}

type ApplicationItemRequestBuilder struct {
	*EntityItemRequestBuilder[ApplicationItemRequestBuilder]
}

func NewApplicationItemRequestBuilder(client *GraphClient, id string) *ApplicationItemRequestBuilder {
	builder := &ApplicationItemRequestBuilder{}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, client, id)

	return builder
}

// Gets a Microsoft Graph Application for the specified application identifier
func (c *ApplicationItemRequestBuilder) Get(ctx context.Context) (*Application, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, fmt.Sprintf("%s/applications/%s", c.client.host, c.id))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, httputil.HandleRequestError(res, err)
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[Application](res)
}

func (c *ApplicationItemRequestBuilder) RemovePassword(ctx context.Context, keyId string) error {
	req, err := runtime.NewRequest(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/applications/%s/removePassword", c.client.host, c.id),
	)
	if err != nil {
		return fmt.Errorf("failed creating request: %w", err)
	}

	removePasswordRequest := ApplicationRemovePasswordRequest{
		KeyId: keyId,
	}

	err = SetHttpRequestBody(req, removePasswordRequest)
	if err != nil {
		return err
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return httputil.HandleRequestError(res, err)
	}

	if !runtime.HasStatusCode(res, http.StatusNoContent) {
		return runtime.NewResponseError(res)
	}

	return nil
}

func (c *ApplicationItemRequestBuilder) AddPassword(ctx context.Context) (*ApplicationPasswordCredential, error) {
	req, err := runtime.NewRequest(ctx, http.MethodPost, fmt.Sprintf("%s/applications/%s/addPassword", c.client.host, c.id))
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	addPasswordRequest := ApplicationAddPasswordRequest{
		PasswordCredential: ApplicationPasswordCredential{
			DisplayName: convert.RefOf("Azure Developer CLI"),
		},
	}

	err = SetHttpRequestBody(req, addPasswordRequest)
	if err != nil {
		return nil, err
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, httputil.HandleRequestError(res, err)
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[ApplicationPasswordCredential](res)
}
