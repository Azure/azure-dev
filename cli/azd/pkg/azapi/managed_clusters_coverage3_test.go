// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v2"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ManagedClustersService_Get_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	svc := NewManagedClustersService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodGet &&
			strings.Contains(req.URL.Path, "/managedClusters/my-aks")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armcontainerservice.ManagedCluster{
				ID:       to.Ptr("/subscriptions/SUB/resourceGroups/RG/providers/Microsoft.ContainerService/managedClusters/my-aks"),
				Name:     to.Ptr("my-aks"),
				Location: to.Ptr("eastus"),
				Properties: &armcontainerservice.ManagedClusterProperties{
					KubernetesVersion: to.Ptr("1.28.0"),
					Fqdn:              to.Ptr("my-aks-dns.hcp.eastus.azmk8s.io"),
				},
			})
	})

	cluster, err := svc.Get(*mockCtx.Context, "SUB", "RG", "my-aks")
	require.NoError(t, err)
	assert.Equal(t, "my-aks", *cluster.Name)
	assert.Equal(t, "1.28.0", *cluster.Properties.KubernetesVersion)
}

func Test_ManagedClustersService_GetUserCredentials_Coverage3(t *testing.T) {
	mockCtx := mocks.NewMockContext(context.Background())
	svc := NewManagedClustersService(mockCtx.SubscriptionCredentialProvider, mockCtx.ArmClientOptions)

	mockCtx.HttpClient.When(func(req *http.Request) bool {
		return req.Method == http.MethodPost &&
			strings.Contains(req.URL.Path, "/listClusterUserCredential")
	}).RespondFn(func(req *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(req, http.StatusOK,
			armcontainerservice.CredentialResults{
				Kubeconfigs: []*armcontainerservice.CredentialResult{
					{
						Name:  to.Ptr("clusterUser"),
						Value: []byte("kubeconfig-data"),
					},
				},
			})
	})

	creds, err := svc.GetUserCredentials(*mockCtx.Context, "SUB", "RG", "my-aks")
	require.NoError(t, err)
	require.Len(t, creds.Kubeconfigs, 1)
	assert.Equal(t, "clusterUser", *creds.Kubeconfigs[0].Name)
}
