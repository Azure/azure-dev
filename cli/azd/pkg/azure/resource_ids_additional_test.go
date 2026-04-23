// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_SubscriptionFromRID(t *testing.T) {
	tests := []struct {
		name  string
		rid   string
		want  string
		panic bool
	}{
		{
			name: "StandardResourceId",
			rid: "/subscriptions/abc-123/resourceGroups/rg/" +
				"providers/Microsoft.Web/sites/myapp",
			want: "abc-123",
		},
		{
			name: "SubscriptionOnly",
			rid:  "/subscriptions/sub-id-456",
			want: "sub-id-456",
		},
		{
			name: "DeploymentResourceId",
			rid: "/subscriptions/deadbeef/providers/" +
				"Microsoft.Resources/deployments/deploy1",
			want: "deadbeef",
		},
		{
			name:  "NoSubscriptionSegment",
			rid:   "/resourceGroups/rg/providers/Microsoft.Web",
			panic: true,
		},
		{
			name:  "EmptyString",
			rid:   "",
			panic: true,
		},
		{
			name:  "SubscriptionsAtEnd",
			rid:   "/something/subscriptions",
			panic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.panic {
				require.Panics(t, func() {
					SubscriptionFromRID(tt.rid)
				})
				return
			}

			got := SubscriptionFromRID(tt.rid)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_SubscriptionRID(t *testing.T) {
	tests := []struct {
		name           string
		subscriptionId string
		want           string
	}{
		{
			name:           "Standard",
			subscriptionId: "abc-123",
			want:           "/subscriptions/abc-123",
		},
		{
			name:           "GuidFormat",
			subscriptionId: "faa080af-c1d8-40ad-9cce-e1a450ca5b57",
			want: "/subscriptions/" +
				"faa080af-c1d8-40ad-9cce-e1a450ca5b57",
		},
		{
			name:           "Empty",
			subscriptionId: "",
			want:           "/subscriptions/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SubscriptionRID(tt.subscriptionId)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_SubscriptionDeploymentRID(t *testing.T) {
	tests := []struct {
		name           string
		subscriptionId string
		deploymentId   string
		want           string
	}{
		{
			name:           "Standard",
			subscriptionId: "sub-1",
			deploymentId:   "deploy-1",
			want: "/subscriptions/sub-1/providers/" +
				"Microsoft.Resources/deployments/deploy-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SubscriptionDeploymentRID(
				tt.subscriptionId, tt.deploymentId,
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_ResourceGroupDeploymentRID(t *testing.T) {
	tests := []struct {
		name              string
		subscriptionId    string
		resourceGroupName string
		deploymentId      string
		want              string
	}{
		{
			name:              "Standard",
			subscriptionId:    "sub-1",
			resourceGroupName: "rg-1",
			deploymentId:      "deploy-1",
			want: "/subscriptions/sub-1" +
				"/resourceGroups/rg-1/providers/" +
				"Microsoft.Resources/deployments/deploy-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResourceGroupDeploymentRID(
				tt.subscriptionId,
				tt.resourceGroupName,
				tt.deploymentId,
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_ResourceGroupRID(t *testing.T) {
	tests := []struct {
		name              string
		subscriptionId    string
		resourceGroupName string
		want              string
	}{
		{
			name:              "Standard",
			subscriptionId:    "sub-1",
			resourceGroupName: "my-rg",
			want: "/subscriptions/sub-1" +
				"/resourceGroups/my-rg",
		},
		{
			name:              "EmptyValues",
			subscriptionId:    "",
			resourceGroupName: "",
			want:              "/subscriptions//resourceGroups/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResourceGroupRID(
				tt.subscriptionId, tt.resourceGroupName,
			)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_WebsiteRID(t *testing.T) {
	got := WebsiteRID("sub-1", "rg-1", "mysite")
	want := "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/Microsoft.Web/sites/mysite"
	require.Equal(t, want, got)
}

func Test_ContainerAppRID(t *testing.T) {
	got := ContainerAppRID("sub-1", "rg-1", "myapp")
	want := "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/Microsoft.App/containerApps/myapp"
	require.Equal(t, want, got)
}

func Test_KubernetesServiceRID(t *testing.T) {
	got := KubernetesServiceRID("sub-1", "rg-1", "mycluster")
	want := "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/Microsoft.ContainerService" +
		"/managedClusters/mycluster"
	require.Equal(t, want, got)
}

func Test_StaticWebAppRID(t *testing.T) {
	got := StaticWebAppRID("sub-1", "rg-1", "mystaticsite")
	want := "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/Microsoft.Web/staticSites/mystaticsite"
	require.Equal(t, want, got)
}

func Test_WorkspaceRID(t *testing.T) {
	got := WorkspaceRID("sub-1", "rg-1", "myworkspace")
	want := "/subscriptions/sub-1/resourceGroups/rg-1" +
		"/providers/" +
		"Microsoft.MachineLearningServices" +
		"/workspaces/myworkspace"
	require.Equal(t, want, got)
}

func Test_RIDRoundTrip(t *testing.T) {
	// Verify that SubscriptionFromRID can extract subscription
	// from any RID builder output.
	subId := "faa080af-c1d8-40ad-9cce-e1a450ca5b57"

	rids := []string{
		WebsiteRID(subId, "rg", "site"),
		ContainerAppRID(subId, "rg", "app"),
		KubernetesServiceRID(subId, "rg", "aks"),
		StaticWebAppRID(subId, "rg", "swa"),
		WorkspaceRID(subId, "rg", "ws"),
		ResourceGroupRID(subId, "rg"),
		SubscriptionDeploymentRID(subId, "dep"),
		ResourceGroupDeploymentRID(subId, "rg", "dep"),
	}

	for _, rid := range rids {
		t.Run(rid, func(t *testing.T) {
			got := SubscriptionFromRID(rid)
			require.Equal(t, subId, got)
		})
	}
}

func Test_GetResourceGroupNameFromRIDBuilders(t *testing.T) {
	tests := []struct {
		name string
		rid  string
		want string
	}{
		{
			name: "FromWebsiteRID",
			rid:  WebsiteRID("sub", "my-rg", "site"),
			want: "my-rg",
		},
		{
			name: "FromContainerAppRID",
			rid:  ContainerAppRID("sub", "my-rg", "app"),
			want: "my-rg",
		},
		{
			name: "FromKubernetesRID",
			rid:  KubernetesServiceRID("sub", "my-rg", "aks"),
			want: "my-rg",
		},
		{
			name: "SubscriptionLevelNoRG",
			rid: "/subscriptions/sub/providers/" +
				"Microsoft.Resources/deployments/d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetResourceGroupName(tt.rid)
			if tt.want == "" {
				require.Nil(t, got)
			} else {
				require.NotNil(t, got)
				require.Equal(t, tt.want, *got)
			}
		})
	}
}
