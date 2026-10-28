// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
)

type delegatingTargetSpy struct {
	initializeErr   error
	initializeHosts []ServiceTargetKind
	toolHosts       []ServiceTargetKind
	packageHosts    []ServiceTargetKind
	publishHosts    []ServiceTargetKind
	deployHosts     []ServiceTargetKind
	endpointHosts   []ServiceTargetKind
	resolveHosts    []ServiceTargetKind
	previewHosts    []ServiceTargetKind
}

func (s *delegatingTargetSpy) Initialize(_ context.Context, config *ServiceConfig) error {
	s.initializeHosts = append(s.initializeHosts, config.Host)
	return s.initializeErr
}

func (s *delegatingTargetSpy) RequiredExternalTools(_ context.Context, config *ServiceConfig) []tools.ExternalTool {
	s.toolHosts = append(s.toolHosts, config.Host)
	return nil
}

func (s *delegatingTargetSpy) Package(
	_ context.Context,
	config *ServiceConfig,
	_ *ServiceContext,
	_ *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	s.packageHosts = append(s.packageHosts, config.Host)
	return &ServicePackageResult{}, nil
}

func (s *delegatingTargetSpy) Publish(
	_ context.Context,
	config *ServiceConfig,
	_ *ServiceContext,
	_ *environment.TargetResource,
	_ *async.Progress[ServiceProgress],
	_ *PublishOptions,
) (*ServicePublishResult, error) {
	s.publishHosts = append(s.publishHosts, config.Host)
	return &ServicePublishResult{}, nil
}

func (s *delegatingTargetSpy) Deploy(
	_ context.Context,
	config *ServiceConfig,
	_ *ServiceContext,
	_ *environment.TargetResource,
	_ *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	s.deployHosts = append(s.deployHosts, config.Host)
	return &ServiceDeployResult{}, nil
}

func (s *delegatingTargetSpy) Endpoints(
	_ context.Context,
	config *ServiceConfig,
	_ *environment.TargetResource,
) ([]string, error) {
	s.endpointHosts = append(s.endpointHosts, config.Host)
	return []string{"https://example.test"}, nil
}

func (s *delegatingTargetSpy) ResolveTargetResource(
	_ context.Context,
	_ string,
	config *ServiceConfig,
	_ func() (*environment.TargetResource, error),
) (*environment.TargetResource, error) {
	s.resolveHosts = append(s.resolveHosts, config.Host)
	return environment.NewTargetResource("sub", "rg", "app", "Microsoft.App/containerApps"), nil
}

func (s *delegatingTargetSpy) PreviewWithTarget(
	_ context.Context,
	config *ServiceConfig,
	_ *environment.TargetResource,
) (*ServiceDeployPreviewResult, error) {
	s.previewHosts = append(s.previewHosts, config.Host)
	return &ServiceDeployPreviewResult{
		Data: map[string]any{"host": config.Host},
	}, nil
}

func TestDelegatingServiceTargetRoutesNativeOperations(t *testing.T) {
	initializer := &delegatingTargetSpy{}
	base := &delegatingTargetSpy{}
	target := NewDelegatingServiceTarget(
		"azure.springboot",
		ContainerAppTarget,
		initializer,
		base,
	)
	config := &ServiceConfig{Host: "azure.springboot"}
	serviceContext := NewServiceContext()
	resource := environment.NewTargetResource("sub", "rg", "app", "Microsoft.App/containerApps")

	require.NoError(t, target.Initialize(t.Context(), config))
	target.RequiredExternalTools(t.Context(), config)
	_, err := target.Package(t.Context(), config, serviceContext, nil)
	require.NoError(t, err)
	_, err = target.Publish(t.Context(), config, serviceContext, resource, nil, &PublishOptions{})
	require.NoError(t, err)
	_, err = target.Deploy(t.Context(), config, serviceContext, resource, nil)
	require.NoError(t, err)
	_, err = target.Endpoints(t.Context(), config, resource)
	require.NoError(t, err)
	_, err = target.(TargetResourceResolver).ResolveTargetResource(
		t.Context(),
		"sub",
		config,
		func() (*environment.TargetResource, error) { return resource, nil },
	)
	require.NoError(t, err)
	preview, err := target.(ServiceTargetResourcePreviewer).PreviewWithTarget(t.Context(), config, resource)
	require.NoError(t, err)

	require.Equal(t, []ServiceTargetKind{"azure.springboot"}, initializer.initializeHosts)
	require.Empty(t, initializer.packageHosts)
	require.Equal(t, []ServiceTargetKind{ContainerAppTarget}, base.initializeHosts)
	require.Equal(t, []ServiceTargetKind{ContainerAppTarget}, base.toolHosts)
	require.Equal(t, []ServiceTargetKind{ContainerAppTarget}, base.packageHosts)
	require.Equal(t, []ServiceTargetKind{ContainerAppTarget}, base.publishHosts)
	require.Equal(t, []ServiceTargetKind{ContainerAppTarget}, base.deployHosts)
	require.Equal(t, []ServiceTargetKind{ContainerAppTarget}, base.endpointHosts)
	require.Equal(t, []ServiceTargetKind{ContainerAppTarget}, base.resolveHosts)
	require.Equal(t, []ServiceTargetKind{ContainerAppTarget}, base.previewHosts)
	require.Equal(t, ServiceTargetKind("azure.springboot"), config.Host)
	require.Equal(t, ServiceTargetKind("azure.springboot"), preview.Data["host"])
}

func TestDelegatingServiceTargetDoesNotFallbackAfterInitializerFailure(t *testing.T) {
	expected := errors.New("invalid Spring configuration")
	initializer := &delegatingTargetSpy{initializeErr: expected}
	base := &delegatingTargetSpy{}
	target := NewDelegatingServiceTarget(
		"azure.springboot",
		ContainerAppTarget,
		initializer,
		base,
	)

	err := target.Initialize(t.Context(), &ServiceConfig{Host: "azure.springboot"})
	require.ErrorIs(t, err, expected)
	require.Empty(t, base.initializeHosts)
}

func TestResolveProviderBaseTargetValidation(t *testing.T) {
	tests := []struct {
		name       string
		host       ServiceTargetKind
		baseTarget *string
		want       ServiceTargetKind
		hasBase    bool
		wantErr    string
	}{
		{name: "omitted"},
		{name: "built in", host: "custom", baseTarget: new("containerapp"), want: ContainerAppTarget, hasBase: true},
		{name: "empty", host: "custom", baseTarget: new(""), wantErr: "empty baseTarget"},
		{name: "whitespace", host: "custom", baseTarget: new(" containerapp"), wantErr: "whitespace"},
		{name: "self", host: "custom", baseTarget: new("custom"), wantErr: "delegate to itself"},
		{name: "other native target", host: "custom", baseTarget: new("appservice"), wantErr: "only containerapp"},
		{name: "extension chain", host: "custom", baseTarget: new("another.extension"), wantErr: "unsupported baseTarget"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var provider *extensions.Provider
			if test.baseTarget != nil {
				provider = &extensions.Provider{BaseTarget: test.baseTarget}
			}
			target, hasBase, err := ResolveProviderBaseTarget(test.host, provider)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, target)
			require.Equal(t, test.hasBase, hasBase)
		})
	}
}

func TestDelegatingServiceTargetPreviewDoesNotMutateNativeResult(t *testing.T) {
	base := &delegatingTargetSpy{}
	target := NewDelegatingServiceTarget(
		"azure.springboot",
		ContainerAppTarget,
		&delegatingTargetSpy{},
		base,
	).(ServiceTargetResourcePreviewer)
	config := &ServiceConfig{Host: "azure.springboot"}

	result, err := target.PreviewWithTarget(t.Context(), config, nil)
	require.NoError(t, err)
	require.Equal(t, ServiceTargetKind("azure.springboot"), result.Data["host"])
	require.Equal(t, []ServiceTargetKind{ContainerAppTarget}, base.previewHosts)
}
