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

// Registration and resolution of an external framework service must succeed regardless of
// whether the environment can be loaded — the environment is resolved lazily per operation.
// Expansion behavior with and without an environment is covered by the
// Test_ExternalFrameworkService_toProtoServiceConfig* tests in pkg/project.
func TestFrameworkService_onRegisterRequest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		lazyEnv *lazy.Lazy[*environment.Environment]
	}{
		{
			name:    "no lazy env",
			lazyEnv: nil,
		},
		{
			name: "env load error",
			lazyEnv: lazy.NewLazy(func() (*environment.Environment, error) {
				return nil, errors.New("no environment")
			}),
		},
		{
			name:    "env available",
			lazyEnv: lazy.From(environment.NewWithValues("test", nil)),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			container := ioc.NewNestedContainer(nil)
			ioc.RegisterInstance[input.Console](container, mockinput.NewMockConsole())

			svc := NewFrameworkService(container, nil, tc.lazyEnv).(*FrameworkService)

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
		})
	}
}

func TestNewServiceTargetService(t *testing.T) {
	t.Parallel()
	container := ioc.NewNestedContainer(nil)
	svc := NewServiceTargetService(container, nil, nil)
	require.NotNil(t, svc)
}
