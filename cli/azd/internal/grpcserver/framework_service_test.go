// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

func TestNewFrameworkService(t *testing.T) {
	t.Parallel()
	container := ioc.NewNestedContainer(nil)
	svc := NewFrameworkService(container, nil, nil)
	require.NotNil(t, svc)
}

func TestFrameworkService_onRegisterRequest_EnvLoadErrorUsesNilEnv(t *testing.T) {
	t.Parallel()

	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance[input.Console](container, mockinput.NewMockConsole())

	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return nil, errors.New("no environment")
	})

	svc := NewFrameworkService(container, nil, lazyEnv).(*FrameworkService)

	var language string
	_, err := svc.onRegisterRequest(
		t.Context(),
		&azdext.RegisterFrameworkServiceRequest{Language: "rust"},
		&extensions.Extension{Id: "test.framework"},
		nil,
		&language,
	)
	require.NoError(t, err)

	var frameworkService project.FrameworkService
	err = container.ResolveNamed("rust", &frameworkService)
	require.NoError(t, err)
	require.NotNil(t, frameworkService)
}

func TestFrameworkService_onRegisterRequest_ResolvesWithEnv(t *testing.T) {
	t.Parallel()

	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance[input.Console](container, mockinput.NewMockConsole())

	lazyEnv := lazy.NewLazy(func() (*environment.Environment, error) {
		return environment.NewWithValues("test", nil), nil
	})

	svc := NewFrameworkService(container, nil, lazyEnv).(*FrameworkService)

	var language string
	_, err := svc.onRegisterRequest(
		t.Context(),
		&azdext.RegisterFrameworkServiceRequest{Language: "rust"},
		&extensions.Extension{Id: "test.framework"},
		nil,
		&language,
	)
	require.NoError(t, err)

	var frameworkService project.FrameworkService
	err = container.ResolveNamed("rust", &frameworkService)
	require.NoError(t, err)
	require.NotNil(t, frameworkService)
}

func TestNewServiceTargetService(t *testing.T) {
	t.Parallel()
	container := ioc.NewNestedContainer(nil)
	svc := NewServiceTargetService(container, nil, nil)
	require.NotNil(t, svc)
}
