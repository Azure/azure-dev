package graphsdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type FederatedIdentityCredentialListRequestBuilder struct {
	*EntityListRequestBuilder[FederatedIdentityCredentialListRequestBuilder]
	applicationId string
}

func NewFederatedIdentityCredentialListRequestBuilder(
	client *GraphClient,
	applicationId string,
) *FederatedIdentityCredentialListRequestBuilder {
	builder := &FederatedIdentityCredentialListRequestBuilder{
		applicationId: applicationId,
	}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, client)

	return builder
}

// Gets a list of applications that the current logged in user has access to.
func (c *FederatedIdentityCredentialListRequestBuilder) Get(
	ctx context.Context,
) (*FederatedIdentityCredentialListResponse, error) {
	req, err := c.createRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/applications/%s/federatedIdentityCredentials", c.client.host, c.applicationId),
	)
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

	return httputil.ReadRawResponse[FederatedIdentityCredentialListResponse](res)
}

func (c *FederatedIdentityCredentialListRequestBuilder) Post(
	ctx context.Context,
	federatedIdentityCredential *FederatedIdentityCredential,
) (*FederatedIdentityCredential, error) {
	req, err := c.createRequest(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/applications/%s/federatedIdentityCredentials", c.client.host, c.applicationId),
	)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	err = SetHttpRequestBody(req, federatedIdentityCredential)
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

	return httputil.ReadRawResponse[FederatedIdentityCredential](res)
}

type FederatedIdentityCredentialItemRequestBuilder struct {
	*EntityItemRequestBuilder[FederatedIdentityCredentialItemRequestBuilder]
	applicationId string
}

func NewFederatedIdentityCredentialItemRequestBuilder(
	client *GraphClient,
	applicationId string,
	id string,
) *FederatedIdentityCredentialItemRequestBuilder {
	builder := &FederatedIdentityCredentialItemRequestBuilder{
		applicationId: applicationId,
	}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, client, id)

	return builder
}

// Gets a Microsoft Graph Application for the specified application identifier
func (c *FederatedIdentityCredentialItemRequestBuilder) Get(ctx context.Context) (*FederatedIdentityCredential, error) {
	req, err := runtime.NewRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/applications/%s/federatedIdentityCredentials/%s", c.client.host, c.applicationId, c.id),
	)
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

	return httputil.ReadRawResponse[FederatedIdentityCredential](res)
}

func (c *FederatedIdentityCredentialItemRequestBuilder) Update(
	ctx context.Context,
	federatedIdentityCredential *FederatedIdentityCredential,
) error {
	req, err := runtime.NewRequest(
		ctx,
		http.MethodPatch,
		fmt.Sprintf("%s/applications/%s/federatedIdentityCredentials/%s", c.client.host, c.applicationId, c.id),
	)
	if err != nil {
		return fmt.Errorf("failed creating request: %w", err)
	}

	err = SetHttpRequestBody(req, federatedIdentityCredential)
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

// Gets a Microsoft Graph Application for the specified application identifier
func (c *FederatedIdentityCredentialItemRequestBuilder) Delete(ctx context.Context) error {
	req, err := runtime.NewRequest(
		ctx,
		http.MethodDelete,
		fmt.Sprintf("%s/applications/%s/federatedIdentityCredentials/%s", c.client.host, c.applicationId, c.id),
	)
	if err != nil {
		return fmt.Errorf("failed creating request: %w", err)
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
