// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package graphsdk

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type MsiFederatedIdentityCredentialListReqBuilder struct {
	*EntityListRequestBuilder[MsiFederatedIdentityCredentialListReqBuilder]
	servicePrincipalId string
}

func NewMsiFederatedIdentityCredentialListRequestBuilder(
	client *GraphClient,
	servicePrincipalId string,
) *MsiFederatedIdentityCredentialListReqBuilder {
	builder := &MsiFederatedIdentityCredentialListReqBuilder{
		servicePrincipalId: servicePrincipalId,
	}
	builder.EntityListRequestBuilder = newEntityListRequestBuilder(builder, client)

	return builder
}

// Gets a list of applications that the current logged in user has access to.
func (c *MsiFederatedIdentityCredentialListReqBuilder) Get(
	ctx context.Context,
) (*FederatedIdentityCredentialListResponse, error) {
	req, err := c.createRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/servicePrincipals/%s/federatedIdentityCredentials", c.client.host, c.servicePrincipalId),
	)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, err
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[FederatedIdentityCredentialListResponse](res)
}

func (c *MsiFederatedIdentityCredentialListReqBuilder) Post(
	ctx context.Context,
	federatedIdentityCredential *FederatedIdentityCredential,
) (*FederatedIdentityCredential, error) {
	req, err := c.createRequest(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/servicePrincipals/%s/federatedIdentityCredentials", c.client.host, c.servicePrincipalId),
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
		return nil, err
	}

	if !runtime.HasStatusCode(res, http.StatusCreated) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[FederatedIdentityCredential](res)
}

type MsiFederatedIdentityCredentialItemRequestBuilder struct {
	*EntityItemRequestBuilder[MsiFederatedIdentityCredentialItemRequestBuilder]
	servicePrincipal string
}

func NewMsiFederatedIdentityCredentialItemRequestBuilder(
	client *GraphClient,
	servicePrincipalId string,
	id string,
) *MsiFederatedIdentityCredentialItemRequestBuilder {
	builder := &MsiFederatedIdentityCredentialItemRequestBuilder{
		servicePrincipal: servicePrincipalId,
	}
	builder.EntityItemRequestBuilder = newEntityItemRequestBuilder(builder, client, id)

	return builder
}

// Gets a Microsoft Graph Application for the specified application identifier
func (c *MsiFederatedIdentityCredentialItemRequestBuilder) Get(ctx context.Context) (*FederatedIdentityCredential, error) {
	req, err := runtime.NewRequest(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/servicePrincipals/%s/federatedIdentityCredentials/%s", c.client.host, c.servicePrincipal, c.id),
	)
	if err != nil {
		return nil, fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return nil, err
	}

	if !runtime.HasStatusCode(res, http.StatusOK) {
		return nil, runtime.NewResponseError(res)
	}

	return httputil.ReadRawResponse[FederatedIdentityCredential](res)
}

func (c *MsiFederatedIdentityCredentialItemRequestBuilder) Update(
	ctx context.Context,
	federatedIdentityCredential *FederatedIdentityCredential,
) error {
	req, err := runtime.NewRequest(
		ctx,
		http.MethodPatch,
		fmt.Sprintf("%s/servicePrincipals/%s/federatedIdentityCredentials/%s", c.client.host, c.servicePrincipal, c.id),
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
		return err
	}

	if !runtime.HasStatusCode(res, http.StatusNoContent) {
		return runtime.NewResponseError(res)
	}

	return nil
}

// Gets a Microsoft Graph Application for the specified application identifier
func (c *MsiFederatedIdentityCredentialItemRequestBuilder) Delete(ctx context.Context) error {
	req, err := runtime.NewRequest(
		ctx,
		http.MethodDelete,
		fmt.Sprintf("%s/servicePrincipals/%s/federatedIdentityCredentials/%s", c.client.host, c.servicePrincipal, c.id),
	)
	if err != nil {
		return fmt.Errorf("failed creating request: %w", err)
	}

	res, err := c.client.pipeline.Do(req)
	if err != nil {
		return err
	}

	if !runtime.HasStatusCode(res, http.StatusNoContent) {
		return runtime.NewResponseError(res)
	}

	return nil
}
