// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Compile-time check that BaseServiceTargetProvider implements ServiceTargetProvider.
var _ ServiceTargetProvider = (*BaseServiceTargetProvider)(nil)

func TestBaseServiceTargetProvider_Initialize(t *testing.T) {
	b := &BaseServiceTargetProvider{}
	err := b.Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)
}

func TestBaseServiceTargetProvider_Endpoints(t *testing.T) {
	b := &BaseServiceTargetProvider{}
	endpoints, err := b.Endpoints(t.Context(), &ServiceConfig{}, &TargetResource{})
	require.NoError(t, err)
	require.Nil(t, endpoints)
}

func TestBaseServiceTargetProvider_GetTargetResource(t *testing.T) {
	b := &BaseServiceTargetProvider{}
	res, err := b.GetTargetResource(t.Context(), "sub-id", &ServiceConfig{}, nil)
	require.NoError(t, err)
	require.Nil(t, res)
}

func TestBaseServiceTargetProvider_Package(t *testing.T) {
	b := &BaseServiceTargetProvider{}
	res, err := b.Package(t.Context(), &ServiceConfig{}, &ServiceContext{}, nil)
	require.NoError(t, err)
	require.Nil(t, res)
}

func TestBaseServiceTargetProvider_Publish(t *testing.T) {
	b := &BaseServiceTargetProvider{}
	res, err := b.Publish(
		t.Context(), &ServiceConfig{}, &ServiceContext{},
		&TargetResource{}, &PublishOptions{}, nil,
	)
	require.NoError(t, err)
	require.Nil(t, res)
}

func TestBaseServiceTargetProvider_Deploy(t *testing.T) {
	b := &BaseServiceTargetProvider{}
	res, err := b.Deploy(t.Context(), &ServiceConfig{}, &ServiceContext{}, &TargetResource{}, nil)
	require.NoError(t, err)
	require.Nil(t, res)
}

// TestBaseServiceTargetProvider_Embedding verifies that a struct embedding
// BaseServiceTargetProvider satisfies the interface and can selectively override methods.
func TestBaseServiceTargetProvider_Embedding(t *testing.T) {
	type customProvider struct {
		BaseServiceTargetProvider
		called bool
	}

	// Override only Deploy
	deploy := func(
		p *customProvider,
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		targetResource *TargetResource,
		progress ProgressReporter,
	) (*ServiceDeployResult, error) {
		p.called = true
		return &ServiceDeployResult{}, nil
	}

	p := &customProvider{}

	// Inherited no-op should work
	err := p.Initialize(t.Context(), &ServiceConfig{})
	require.NoError(t, err)

	// Custom deploy should work
	res, err := deploy(p, t.Context(), &ServiceConfig{}, &ServiceContext{}, &TargetResource{}, nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, p.called)
}
