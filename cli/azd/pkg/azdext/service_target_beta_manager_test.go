// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestStableServiceTargetRegisterSignature(t *testing.T) {
	var manager ServiceTargetManager
	var register func(context.Context, ServiceTargetFactory, string) error = manager.Register
	require.NotNil(t, register)
	var _ serviceTargetRegistrar = &manager
}

func TestBetaServiceTargetManager_StableLifecycle(t *testing.T) {
	manager := NewBetaServiceTargetManager("test.ext", nil, nil)
	provider := &MockServiceTargetProvider{}
	manager.handler.componentManager.RegisterFactory("custom", func() ServiceTargetProvider { return provider })
	stableConfig := &ServiceConfig{Name: "web", Host: "custom"}
	config := &v1beta.ServiceConfig{Name: "web", Host: "custom"}
	matchesConfig := mock.MatchedBy(func(actual *ServiceConfig) bool { return proto.Equal(stableConfig, actual) })
	provider.On("Initialize", t.Context(), matchesConfig).Return(nil).Once()
	response, err := manager.onInitialize(t.Context(), &v1beta.ServiceTargetInitializeRequest{ServiceConfig: config})
	require.NoError(t, err)
	require.NotNil(t, response.GetInitializeResponse())

	target := &TargetResource{SubscriptionId: "subscription", ResourceGroupName: "group"}
	provider.On("GetTargetResource", t.Context(), "subscription", matchesConfig, mock.Anything).Return(target, nil).Once()
	response, err = manager.onGetTargetResource(t.Context(), &v1beta.GetTargetResourceRequest{
		SubscriptionId: "subscription", ServiceConfig: config,
	})
	require.NoError(t, err)
	require.Equal(t, "group", response.GetGetTargetResourceResponse().TargetResource.ResourceGroupName)

	provider.On("Package", t.Context(), matchesConfig, (*ServiceContext)(nil), mock.Anything).
		Return(&ServicePackageResult{}, nil).Once()
	response, err = manager.onPackage(t.Context(), &v1beta.ServiceTargetPackageRequest{ServiceConfig: config}, nil)
	require.NoError(t, err)
	require.NotNil(t, response.GetPackageResponse().Result)

	provider.On("Publish", t.Context(), matchesConfig, (*ServiceContext)(nil),
		(*TargetResource)(nil), (*PublishOptions)(nil), mock.Anything).
		Return(&ServicePublishResult{}, nil).Once()
	response, err = manager.onPublish(t.Context(), &v1beta.ServiceTargetPublishRequest{ServiceConfig: config}, nil)
	require.NoError(t, err)
	require.NotNil(t, response.GetPublishResponse().Result)

	provider.On("Deploy", t.Context(), matchesConfig, (*ServiceContext)(nil), (*TargetResource)(nil), mock.Anything).
		Return(&ServiceDeployResult{}, nil).Once()
	response, err = manager.onDeploy(t.Context(), &v1beta.ServiceTargetDeployRequest{ServiceConfig: config}, nil)
	require.NoError(t, err)
	require.NotNil(t, response.GetDeployResponse().Result)

	provider.On("Endpoints", t.Context(), matchesConfig, (*TargetResource)(nil)).
		Return([]string{"https://example.com"}, nil).Once()
	response, err = manager.onEndpoints(t.Context(), &v1beta.ServiceTargetEndpointsRequest{ServiceConfig: config})
	require.NoError(t, err)
	require.Equal(t, []string{"https://example.com"}, response.GetEndpointsResponse().Endpoints)
	provider.AssertExpectations(t)
}
