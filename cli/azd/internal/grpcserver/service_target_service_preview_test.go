// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/stretchr/testify/require"
)

type previewRegistrationPrompter struct {
	prompt.Prompter
}

func TestBetaServiceTargetServiceRegistersPreviewCapability(t *testing.T) {
	t.Parallel()
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance[input.Console](container, mockinput.NewMockConsole())
	ioc.RegisterInstance[prompt.Prompter](container, &previewRegistrationPrompter{})
	service := NewServiceTargetService(container, nil, nil).(*ServiceTargetService)
	override := &betaServiceTargetServiceOverride{service: service}
	host := ""
	response, err := override.onRegisterRequest(
		t.Context(),
		&v1beta.RegisterServiceTargetRequest{Host: "custom", SupportsPreview: true},
		&extensions.Extension{Id: "test.extension"},
		nil,
		&host,
	)
	require.NoError(t, err)
	require.NotNil(t, response.GetRegisterServiceTargetResponse())
	require.Equal(t, "custom", host)

	var target project.ServiceTarget
	require.NoError(t, container.ResolveNamed(host, &target))
	capability, ok := target.(project.ServiceTargetPreviewCapability)
	require.True(t, ok)
	require.True(t, capability.SupportsPreview())
	previewer, ok := target.(project.ServiceTargetPreviewer)
	require.True(t, ok)
	_, err = previewer.Preview(t.Context(), nil)
	require.ErrorContains(t, err, "service configuration is required")
}

func TestBetaServiceTargetServiceRejectsMissingPreviewCapability(t *testing.T) {
	t.Parallel()
	service := NewServiceTargetService(ioc.NewNestedContainer(nil), nil, nil).(*ServiceTargetService)
	override := &betaServiceTargetServiceOverride{service: service}
	_, err := override.onRegisterRequest(
		t.Context(),
		&v1beta.RegisterServiceTargetRequest{Host: "custom"},
		&extensions.Extension{Id: "test.extension"},
		nil,
		new(string),
	)
	require.ErrorContains(t, err, "must advertise deployment preview support")
}
