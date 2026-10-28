// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestServiceTargetServiceRegistersDelegatedTargetInActiveAndSharedScopes(t *testing.T) {
	active := newServiceTargetTestContainer()
	shared := newServiceTargetTestContainer()
	ioc.RegisterNamedInstance[project.ServiceTarget](active, "containerapp", &registrationTestTarget{})
	ioc.RegisterNamedInstance[project.ServiceTarget](shared, "containerapp", &registrationTestTarget{})

	baseTarget := "containerapp"
	extension := &extensions.Extension{
		Id: "azure.springboot",
		Providers: []extensions.Provider{{
			Name:       "azure.springboot",
			Type:       extensions.ServiceTargetProviderType,
			BaseTarget: &baseTarget,
		}},
	}
	service := NewSharedServiceTargetService(active, shared, nil, nil).(*ServiceTargetService)

	var registered string
	response, err := service.onRegisterRequest(
		t.Context(),
		&azdext.RegisterServiceTargetRequest{Host: "azure.springboot"},
		extension,
		nil,
		&registered,
	)
	require.NoError(t, err)
	require.NotNil(t, response.GetRegisterServiceTargetResponse())
	require.Equal(t, "azure.springboot", registered)

	for name, container := range map[string]*ioc.NestedContainer{
		"active": active,
		"shared": shared,
	} {
		t.Run(name, func(t *testing.T) {
			var target project.ServiceTarget
			require.NoError(t, container.ResolveNamed("azure.springboot", &target))
			require.Contains(t, fmt.Sprintf("%T", target), "delegatingServiceTarget")
		})
	}
}

func TestServiceTargetServicePreservesNonDelegatingRegistration(t *testing.T) {
	container := newServiceTargetTestContainer()
	extension := &extensions.Extension{Id: "legacy.target"}
	service := newServiceTargetService(container, nil, nil, nil).(*ServiceTargetService)

	var registered string
	_, err := service.onRegisterRequest(
		t.Context(),
		&azdext.RegisterServiceTargetRequest{Host: "legacy.target"},
		extension,
		nil,
		&registered,
	)
	require.NoError(t, err)

	var target project.ServiceTarget
	require.NoError(t, container.ResolveNamed("legacy.target", &target))
	require.Equal(t, "*project.ExternalServiceTarget", fmt.Sprintf("%T", target))
}

func TestServiceTargetServiceRegistrationErrors(t *testing.T) {
	t.Run("duplicate provider metadata", func(t *testing.T) {
		container := newServiceTargetTestContainer()
		service := newServiceTargetService(container, nil, nil, nil).(*ServiceTargetService)
		extension := &extensions.Extension{
			Id: "duplicate.target",
			Providers: []extensions.Provider{
				{Name: "duplicate.target", Type: extensions.ServiceTargetProviderType},
				{Name: "duplicate.target", Type: extensions.ServiceTargetProviderType},
			},
		}

		var registered string
		_, err := service.onRegisterRequest(
			t.Context(),
			&azdext.RegisterServiceTargetRequest{Host: "duplicate.target"},
			extension,
			nil,
			&registered,
		)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		require.Empty(t, registered)
	})

	t.Run("invalid base target metadata", func(t *testing.T) {
		container := newServiceTargetTestContainer()
		service := newServiceTargetService(container, nil, nil, nil).(*ServiceTargetService)
		baseTarget := "same.target"
		extension := &extensions.Extension{
			Id: "same.target",
			Providers: []extensions.Provider{{
				Name:       "same.target",
				Type:       extensions.ServiceTargetProviderType,
				BaseTarget: &baseTarget,
			}},
		}

		var registered string
		_, err := service.onRegisterRequest(
			t.Context(),
			&azdext.RegisterServiceTargetRequest{Host: "same.target"},
			extension,
			nil,
			&registered,
		)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		require.Empty(t, registered)
	})

	t.Run("missing native base target", func(t *testing.T) {
		container := newServiceTargetTestContainer()
		service := newServiceTargetService(container, nil, nil, nil).(*ServiceTargetService)
		baseTarget := "containerapp"
		extension := &extensions.Extension{
			Id: "missing.base",
			Providers: []extensions.Provider{{
				Name:       "missing.base",
				Type:       extensions.ServiceTargetProviderType,
				BaseTarget: &baseTarget,
			}},
		}

		var registered string
		_, err := service.onRegisterRequest(
			t.Context(),
			&azdext.RegisterServiceTargetRequest{Host: "missing.base"},
			extension,
			nil,
			&registered,
		)
		require.NoError(t, err)

		var target project.ServiceTarget
		err = container.ResolveNamed("missing.base", &target)
		require.ErrorContains(t, err, `resolving base service target "containerapp"`)
	})
}

func newServiceTargetTestContainer() *ioc.NestedContainer {
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance[input.Console](container, mockinput.NewMockConsole())
	ioc.RegisterInstance[prompt.Prompter](container, &registrationTestPrompter{})
	return container
}

type registrationTestPrompter struct{}

func (*registrationTestPrompter) PromptSubscription(context.Context, string) (string, error) {
	return "", nil
}

func (*registrationTestPrompter) PromptLocation(
	context.Context,
	string,
	string,
	prompt.LocationFilterPredicate,
	*string,
) (string, error) {
	return "", nil
}

func (*registrationTestPrompter) PromptResourceGroup(
	context.Context,
	prompt.PromptResourceOptions,
) (string, error) {
	return "", nil
}

func (*registrationTestPrompter) PromptResourceGroupFrom(
	context.Context,
	string,
	string,
	prompt.PromptResourceGroupFromOptions,
) (string, error) {
	return "", nil
}

func (*registrationTestPrompter) IsNoPromptMode() bool {
	return true
}

type registrationTestTarget struct{}

func (*registrationTestTarget) Initialize(context.Context, *project.ServiceConfig) error {
	return nil
}

func (*registrationTestTarget) RequiredExternalTools(
	context.Context,
	*project.ServiceConfig,
) []tools.ExternalTool {
	return nil
}

func (*registrationTestTarget) Package(
	context.Context,
	*project.ServiceConfig,
	*project.ServiceContext,
	*async.Progress[project.ServiceProgress],
) (*project.ServicePackageResult, error) {
	return nil, nil
}

func (*registrationTestTarget) Publish(
	context.Context,
	*project.ServiceConfig,
	*project.ServiceContext,
	*environment.TargetResource,
	*async.Progress[project.ServiceProgress],
	*project.PublishOptions,
) (*project.ServicePublishResult, error) {
	return nil, nil
}

func (*registrationTestTarget) Deploy(
	context.Context,
	*project.ServiceConfig,
	*project.ServiceContext,
	*environment.TargetResource,
	*async.Progress[project.ServiceProgress],
) (*project.ServiceDeployResult, error) {
	return nil, nil
}

func (*registrationTestTarget) Endpoints(
	context.Context,
	*project.ServiceConfig,
	*environment.TargetResource,
) ([]string, error) {
	return nil, nil
}
