// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

func TestServiceTargetServiceRegistersPreviewCapability(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name            string
		supportsPreview bool
		wantError       string
	}{
		{name: "LegacyProvider", wantError: "does not support deployment preview"},
		{name: "PreviewProvider", supportsPreview: true, wantError: "service configuration is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			container := ioc.NewNestedContainer(nil)
			ioc.RegisterInstance[input.Console](container, mockinput.NewMockConsole())
			ioc.RegisterInstance[prompt.Prompter](container, &previewRegistrationPrompter{})
			server := NewServiceTargetService(container, nil, nil).(*ServiceTargetService)
			req := &azdext.RegisterServiceTargetRequest{Host: "custom", SupportsPreview: tc.supportsPreview}
			host := ""
			response, err := server.onRegisterRequest(
				t.Context(), req, &extensions.Extension{Id: "test.extension"}, nil, &host,
			)
			require.NoError(t, err)
			require.NotNil(t, response.GetRegisterServiceTargetResponse())
			require.Equal(t, "custom", host)

			// Registration captures the negotiated capability, not the mutable request object.
			req.SupportsPreview = !req.SupportsPreview
			var target project.ServiceTarget
			require.NoError(t, container.ResolveNamed(host, &target))
			previewer, ok := target.(project.ServiceTargetPreviewer)
			require.True(t, ok)
			_, err = previewer.Preview(t.Context(), nil)
			require.ErrorContains(t, err, tc.wantError)
		})
	}
}
