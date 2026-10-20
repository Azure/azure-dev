// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
)

const registryAPIVersion = "2025-11-01"

// The shared stable SDK predates authorization mode. Keep the extension's mode contract
// typed against the documented registry ARM contract.
type registryResource struct {
	armcontainerregistry.Registry
	AuthorizationMode *string
}

func (r *registryResource) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &r.Registry); err != nil {
		return err
	}
	var mode struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &mode); err != nil {
		return err
	}
	if value, present := mode.Properties["roleAssignmentMode"]; present {
		return json.Unmarshal(value, &r.AuthorizationMode)
	}
	return nil
}

type registryCreateProperties struct {
	AdminUserEnabled     bool   `json:"adminUserEnabled"`
	AnonymousPullEnabled bool   `json:"anonymousPullEnabled"`
	PublicNetworkAccess  string `json:"publicNetworkAccess"`
	RoleAssignmentMode   string `json:"roleAssignmentMode"`
}

type registryCreateRequest struct {
	Location   string                    `json:"location"`
	Tags       map[string]*string        `json:"tags"`
	SKU        *armcontainerregistry.SKU `json:"sku"`
	Properties *registryCreateProperties `json:"properties"`
}

type registryARMClient struct {
	client *arm.Client
}

func newRegistryARMClient(credential azcore.TokenCredential, options *arm.ClientOptions) (*registryARMClient, error) {
	client, err := arm.NewClient("azure.ai.projects.registry", "v1.0.0", credential, onboardingARMOptions(options))
	if err != nil {
		return nil, err
	}
	return &registryARMClient{client: client}, nil
}

func (c *registryARMClient) request(
	ctx context.Context, method, id string, body *registryCreateRequest,
) (*http.Response, error) {
	request, err := runtime.NewRequest(ctx, method, runtime.JoinPaths(c.client.Endpoint(), id))
	if err != nil {
		return nil, err
	}
	query := request.Raw().URL.Query()
	query.Set("api-version", registryAPIVersion)
	request.Raw().URL.RawQuery = query.Encode()
	if body != nil {
		if err := runtime.MarshalAsJSON(request, body); err != nil {
			return nil, err
		}
	}
	response, err := c.client.Pipeline().Do(request)
	if err != nil {
		return nil, err
	}
	if !runtime.HasStatusCode(response, http.StatusOK, http.StatusCreated, http.StatusAccepted) {
		return nil, runtime.NewResponseError(response)
	}
	return response, nil
}

func (c *registryARMClient) get(ctx context.Context, id string) (registryResource, error) {
	response, err := c.request(ctx, http.MethodGet, id, nil)
	if err != nil {
		return registryResource{}, err
	}
	var resource registryResource
	err = runtime.UnmarshalAsJSON(response, &resource)
	return resource, err
}

func (c *registryARMClient) create(ctx context.Context, id string, body registryCreateRequest) error {
	response, err := c.request(ctx, http.MethodPut, id, &body)
	if err != nil {
		return err
	}
	poller, err := runtime.NewPoller[registryResource](response, c.client.Pipeline(), nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}
